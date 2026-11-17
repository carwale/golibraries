//go:build integration

// Integration tests against a real Valkey (7+). miniredis has no sharded pub/sub or cluster, so
// these cover SSUBSCRIBE/SPUBLISH and cross-slot reads.
//
//	VALKEY_ADDR=localhost:6379 go test -tags integration ./valkey/...
//	VALKEY_ADDR=localhost:7000 VALKEY_CLUSTER=true go test -tags integration ./valkey/...
package valkey

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/carwale/golibraries/valkey/pubsub"
)

func integrationClient(t *testing.T) *Client {
	t.Helper()
	addr := os.Getenv("VALKEY_ADDR")
	if addr == "" {
		t.Skip("VALKEY_ADDR not set")
	}
	cfg := localConfig(t, addr)
	cfg.IsCluster = Bool(os.Getenv("VALKEY_CLUSTER") == "true")
	cfg.ShardedPubSub = Bool(true) // SSUBSCRIBE works on a single Valkey 7+ node too
	cfg.RequestTimeout = 2 * time.Second
	c, err := NewClient(cfg, WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestIntegrationShardedPubSub(t *testing.T) {
	c := integrationClient(t)
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx := context.Background()

	prefix := fmt.Sprintf("it:%d", time.Now().UnixNano())
	channels := []string{prefix + ":ch:pod:a", prefix + ":ch:pod:b", prefix + ":ch:pod:c"}
	got := make(chan pubsub.Message, 10)
	s, err := sub.Subscribe(ctx, channels, func(m pubsub.Message) { got <- m })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()

	errs := c.Publisher().PublishMulti(ctx, []pubsub.OutboundMessage{
		{Channel: channels[0], Payload: []byte("a")},
		{Channel: channels[1], Payload: []byte("b")},
		{Channel: channels[2], Payload: []byte("c")},
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("SPUBLISH %d: %v", i, err)
		}
	}
	seen := map[string]bool{}
	for len(seen) < 3 {
		select {
		case m := <-got:
			seen[string(m.Payload)] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("received %v, want a, b and c", seen)
		}
	}
}

func TestIntegrationCrossSlotReads(t *testing.T) {
	c := integrationClient(t)
	ctx := context.Background()
	prefix := fmt.Sprintf("it:%d", time.Now().UnixNano())
	keys := []string{prefix + ":a", prefix + ":b", prefix + ":c", prefix + ":d"}

	for i, k := range keys {
		if !c.Cache().Set(ctx, k, []byte{byte('0' + i)}, time.Minute) {
			t.Fatalf("Set %s failed", k)
		}
	}
	defer c.Store().Del(ctx, keys...)

	if got := c.Cache().MultiGet(ctx, keys); len(got) != len(keys) {
		t.Fatalf("MultiGet returned %d of %d keys", len(got), len(keys))
	}
	if got, err := c.Store().MGet(ctx, keys); err != nil || len(got) != len(keys) {
		t.Fatalf("Store MGet = %d keys, %v", len(got), err)
	}
	if !c.Cache().ExpireMultiple(ctx, keys) {
		t.Fatal("ExpireMultiple across slots failed")
	}
	if n, err := c.Store().Exists(ctx, keys...); err != nil || n != int64(len(keys)) {
		t.Fatalf("Exists = %d, %v", n, err)
	}
}
