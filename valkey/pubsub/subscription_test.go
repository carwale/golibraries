package pubsub

import (
	"context"
	"errors"
	"github.com/alicebob/miniredis/v2"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carwale/golibraries/valkey/internal/testutil"
	valkeygo "github.com/valkey-io/valkey-go"
)

// Closing one subscription must not unsubscribe another subscription to the same channel: valkey-go's
// UNSUBSCRIBE ends every receiver of the channel on the shared connection.
func TestCloseKeepsOtherSubscriptionsOnSameChannel(t *testing.T) {
	pub, sub, _ := newTestPubSub(t)
	ctx := context.Background()

	gotA := make(chan Message, 10)
	gotB := make(chan Message, 10)
	var dropped atomic.Int32
	a, err := sub.Subscribe(ctx, []string{"shared", "only-a"}, func(m Message) { gotA <- m })
	if err != nil {
		t.Fatalf("Subscribe a: %v", err)
	}
	if _, err := sub.Subscribe(ctx, []string{"shared"}, func(m Message) { gotB <- m },
		OnDisconnected(func(error) { dropped.Add(1) })); err != nil {
		t.Fatalf("Subscribe b: %v", err)
	}

	if _, err := pub.Publish(ctx, "shared", []byte("both")); err != nil {
		t.Fatal(err)
	}
	expectMessage(t, gotA)
	expectMessage(t, gotB)

	a.Close()
	// "only-a" had no other subscription, so it was unsubscribed; "shared" keeps b.
	if n, _ := pub.Publish(ctx, "only-a", []byte("x")); n != 0 {
		t.Fatalf("only-a should have no subscribers after Close, got %d", n)
	}
	n, err := pub.Publish(ctx, "shared", []byte("after"))
	if err != nil || n != 1 {
		t.Fatalf("Publish shared after closing a = %d, %v; want 1 subscriber", n, err)
	}
	if m := expectMessage(t, gotB); string(m.Payload) != "after" {
		t.Fatalf("b got %q", m.Payload)
	}
	time.Sleep(100 * time.Millisecond)
	if d := dropped.Load(); d != 0 {
		t.Fatalf("b saw %d disconnects after a closed", d)
	}
}

func TestHandlerPanicIsRecovered(t *testing.T) {
	client, mr := testutil.NewClient(t)
	opt := testutil.Option(mr.Addr())
	opt.DisableRetry = true
	logger, logs := testutil.BufferLogger()
	sub := NewSubscriber(testutil.Connect(t, opt), WithSubscriberLogger(logger))
	t.Cleanup(sub.Close)
	pub := NewPublisher(client)
	ctx := context.Background()

	got := make(chan Message, 10)
	if _, err := sub.Subscribe(ctx, []string{"ch"}, func(m Message) {
		if string(m.Payload) == "boom" {
			panic("bad message")
		}
		got <- m
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := pub.Publish(ctx, "ch", []byte("boom")); err != nil {
		t.Fatal(err)
	}
	if _, err := pub.Publish(ctx, "ch", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if m := expectMessage(t, got); string(m.Payload) != "ok" {
		t.Fatalf("message after panic = %q", m.Payload)
	}
	if !strings.Contains(logs.String(), "panicked") {
		t.Fatalf("panic not logged; logs: %s", logs.String())
	}
}

func TestBlankChannelsAreRejected(t *testing.T) {
	pub, sub, _ := newTestPubSub(t)
	ctx := context.Background()
	h := func(Message) {}

	if _, err := sub.Subscribe(ctx, []string{"ok", " "}, h); !errors.Is(err, ErrEmptyChannel) {
		t.Fatalf("Subscribe with a blank channel = %v, want ErrEmptyChannel", err)
	}
	if _, err := sub.PSubscribe(ctx, []string{""}, h); !errors.Is(err, ErrEmptyChannel) {
		t.Fatalf("PSubscribe with a blank pattern = %v, want ErrEmptyChannel", err)
	}
	if _, err := pub.Publish(ctx, "", nil); !errors.Is(err, ErrEmptyChannel) {
		t.Fatalf("Publish blank = %v, want ErrEmptyChannel", err)
	}
	if errs := pub.PublishMulti(ctx, []OutboundMessage{{Channel: " "}}); !errors.Is(errs[0], ErrEmptyChannel) {
		t.Fatalf("PublishMulti blank = %v, want ErrEmptyChannel", errs[0])
	}
}

// A Subscribe that never gets confirmed reports why, not only the deadline.
func TestSubscribeTimeoutWrapsLastError(t *testing.T) {
	_, sub, mr := newTestPubSub(t)
	mr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := sub.Subscribe(ctx, []string{"ch"}, func(Message) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Subscribe = %v, want a deadline error", err)
	}
	if !strings.Contains(err.Error(), "last error:") {
		t.Fatalf("Subscribe error does not carry the receive error: %v", err)
	}
}

// miniredis has no sharded pub/sub, so check the command selection without a server. The
// integration tests (build tag integration) cover SPUBLISH/SSUBSCRIBE end to end.
func TestShardedCommandSelection(t *testing.T) {
	client, _ := testutil.NewClient(t)
	cmd := func(c valkeygo.Completed) string { return c.Commands()[0] }

	sharded := NewPublisher(client, WithShardedPublish(true)).(*publisher)
	classic := NewPublisher(client).(*publisher)
	if got := cmd(sharded.cmd("ch", nil, publishOptions{})); got != "SPUBLISH" {
		t.Fatalf("sharded publish = %s", got)
	}
	if got := cmd(sharded.cmd("ch", nil, applyPublishOptions([]PublishOption{WithClassicPublish()}))); got != "PUBLISH" {
		t.Fatalf("sharded publish with WithClassicPublish = %s", got)
	}
	if got := cmd(classic.cmd("ch", nil, publishOptions{})); got != "PUBLISH" {
		t.Fatalf("classic publish = %s", got)
	}

	s := NewSubscriber(client, WithShardedSubscribe(true)).(*subscriber)
	loops := s.channelLoops([]string{"a", "b"}, subscribeOptions{})
	if len(loops) != 2 {
		t.Fatalf("sharded loops = %d, want one per channel", len(loops))
	}
	for _, l := range loops {
		if got := cmd(l.build(client.B())); got != "SSUBSCRIBE" {
			t.Fatalf("sharded subscribe = %s", got)
		}
		if got := cmd(buildUnsubscribe(client.B(), l.kind, l.names)); got != "SUNSUBSCRIBE" {
			t.Fatalf("sharded unsubscribe = %s", got)
		}
	}
	off := false
	loops = s.channelLoops([]string{"a", "b"}, subscribeOptions{sharded: &off})
	if len(loops) != 1 || cmd(loops[0].build(client.B())) != "SUBSCRIBE" {
		t.Fatalf("WithSharded(false) should give one SUBSCRIBE loop, got %d", len(loops))
	}
	if got := cmd(buildUnsubscribe(client.B(), "psubscribe", []string{"p:*"})); got != "PUNSUBSCRIBE" {
		t.Fatalf("pattern unsubscribe = %s", got)
	}
}

// Server replies that a retry cannot fix fail a starting Subscribe at once (the fail-fast path itself
// is covered against a real ACL by TestIntegrationSubscribeRejectionFailsFast); transient replies
// and network errors are retried.
func TestRejectedClassifiesServerErrors(t *testing.T) {
	mr := miniredis.RunT(t)
	opt := testutil.Option(mr.Addr())
	opt.DisableRetry = true // valkey-go otherwise retries LOADING / TRYAGAIN / CLUSTERDOWN on Do
	client := testutil.Connect(t, opt)
	serverErr := func(reply string) error {
		mr.SetError(reply)
		defer mr.SetError("")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return client.Do(ctx, client.B().Get().Key("k").Build()).Error()
	}
	for reply, want := range map[string]bool{
		"NOPERM User app has no permissions to run the 'subscribe' command": true,
		"NOAUTH Authentication required.":                                   true,
		"WRONGPASS invalid username-password pair or user is disabled.":     true,
		"ERR max number of clients reached":                                 false,
		"LOADING Valkey is loading the dataset in memory":                   false,
		"TRYAGAIN Multiple keys request during rehashing of slot":           false,
		"CLUSTERDOWN The cluster is down":                                   false,
	} {
		err := serverErr(reply)
		if got := rejected(err); got != want {
			t.Errorf("rejected(%T %v) = %v, want %v", err, err, got, want)
		}
	}
	if rejected(errors.New("dial tcp: connection refused")) || rejected(nil) {
		t.Error("network errors and nil must be retried")
	}
}

// One outage is one OnDisconnected, however many reconnect attempts fail before it recovers.
func TestOnDisconnectedOncePerOutage(t *testing.T) {
	_, sub, mr := newTestPubSub(t)

	var disconnects atomic.Int32
	resubscribed := make(chan string, 10)
	_, err := sub.Subscribe(context.Background(), []string{"ch"}, func(Message) {},
		OnDisconnected(func(error) { disconnects.Add(1) }),
		OnResubscribed(func(ch string) { resubscribed <- ch }),
		WithBackoff(10*time.Millisecond, 20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	mr.Close()
	time.Sleep(300 * time.Millisecond) // many failed reconnect attempts
	if err := mr.Restart(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-resubscribed:
	case <-time.After(5 * time.Second):
		t.Fatal("OnResubscribed was not called")
	}
	if n := disconnects.Load(); n != 1 {
		t.Fatalf("OnDisconnected called %d times for one outage, want 1", n)
	}
}
