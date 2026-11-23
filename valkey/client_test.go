package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/carwale/golibraries/valkey/cache"
	"github.com/carwale/golibraries/valkey/pubsub"
	"github.com/carwale/golibraries/valkey/store"
)

func TestNewClientRejectsInvalidConfig(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("NewClient with empty config should fail")
	}
}

func TestNewClientFailsWhenUnreachable(t *testing.T) {
	cfg := Config{Host: "127.0.0.1", Port: 1, IsCluster: Bool(false), TLS: Bool(false), AuthMode: AuthNone}
	if _, err := NewClient(cfg, WithLogger(discardLogger())); err == nil {
		t.Fatal("NewClient to a closed port should fail")
	}
}

// TestClientWiring checks that the Client passes its config to the cache, store and pub/sub views.
func TestClientWiring(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := localConfig(t, mr.Addr())
	// Not cache.DefaultRefreshDuration, so the TTL check below fails if NewClient drops the setting.
	cfg.RefreshCacheDuration = 7 * time.Second
	c, err := NewClient(cfg, WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(c.Close)
	ctx := context.Background()

	if got := c.Config(); got.RequestTimeout != 250*time.Millisecond || got.Port == 0 {
		t.Fatalf("Config defaults not filled: %+v", got)
	}

	// Cache uses RefreshCacheDuration for soft expire.
	type item struct{ N int }
	if !cache.SetAs(ctx, c.Cache(), "k", item{N: 1}, time.Hour) || !c.Cache().Expire(ctx, "k") {
		t.Fatal("cache set/expire failed")
	}
	if ttl := mr.TTL("k"); ttl != 7*time.Second {
		t.Fatalf("soft expire TTL = %v, want the configured 7s refresh duration", ttl)
	}

	// Store shares the connection and returns errors.
	if err := store.SetAs(ctx, c.Store(), "s", item{N: 2}, 0); err != nil {
		t.Fatal(err)
	}
	if v, err := store.GetAs[item](ctx, c.Store(), "s"); err != nil || v.N != 2 {
		t.Fatalf("store.GetAs = %+v, %v", v, err)
	}

	// Publisher and subscriber reach each other (classic pub/sub, IsCluster false).
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	got := make(chan pubsub.Message, 1)
	if _, err := sub.Subscribe(ctx, []string{"ch"}, func(m pubsub.Message) { got <- m }); err != nil {
		t.Fatal(err)
	}
	if n, err := c.Publisher().Publish(ctx, "ch", []byte("hi")); err != nil || n != 1 {
		t.Fatalf("Publish = %d, %v", n, err)
	}
	select {
	case m := <-got:
		if string(m.Payload) != "hi" {
			t.Fatalf("message = %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message")
	}

	if c.Raw() == nil {
		t.Fatal("Raw should expose the valkey-go client")
	}
}
