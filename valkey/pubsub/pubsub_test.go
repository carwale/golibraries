package pubsub

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/carwale/golibraries/valkey/internal/testutil"
	valkeygo "github.com/valkey-io/valkey-go"
)

// newTestPubSub starts miniredis with a publisher on one client and a subscriber on its own client
// (DisableRetry, as valkey.Client.NewSubscriber sets it).
func newTestPubSub(t *testing.T) (IPublisher, ISubscriber, *miniredis.Miniredis) {
	t.Helper()
	client, mr := testutil.NewClient(t)
	opt := testutil.Option(mr.Addr())
	opt.DisableRetry = true
	subClient, err := valkeygo.NewClient(opt)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	sub := NewSubscriber(subClient, WithSubscriberLogger(testutil.DiscardLogger()))
	t.Cleanup(sub.Close)
	return NewPublisher(client), sub, mr
}

func expectMessage(t *testing.T, ch <-chan Message) Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a message")
		return Message{}
	}
}

func TestSubscribeAndPublish(t *testing.T) {
	pub, sub, _ := newTestPubSub(t)
	ctx := context.Background()

	var mu sync.Mutex
	var confirmed []string
	got := make(chan Message, 10)
	s, err := sub.Subscribe(ctx, []string{"ch:pod:a", "ch:pod:b"}, func(m Message) { got <- m },
		OnSubscribed(func(ch string) { mu.Lock(); confirmed = append(confirmed, ch); mu.Unlock() }))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if chs := s.Channels(); len(chs) != 2 {
		t.Fatalf("Channels = %v", chs)
	}

	n, err := pub.Publish(ctx, "ch:pod:a", []byte(`{"kind":"new_message"}`))
	if err != nil || n != 1 {
		t.Fatalf("Publish = %d, %v", n, err)
	}
	m := expectMessage(t, got)
	if m.Channel != "ch:pod:a" || string(m.Payload) != `{"kind":"new_message"}` || m.Pattern != "" {
		t.Fatalf("message = %+v", m)
	}

	errs := pub.PublishMulti(ctx, []OutboundMessage{
		{Channel: "ch:pod:a", Payload: []byte("1")},
		{Channel: "", Payload: []byte("bad")},
		{Channel: "ch:pod:b", Payload: []byte("2")},
	})
	if errs[0] != nil || errs[1] == nil || errs[2] != nil {
		t.Fatalf("PublishMulti errs = %v", errs)
	}
	first, second := expectMessage(t, got), expectMessage(t, got)
	if string(first.Payload) != "1" || string(second.Payload) != "2" {
		t.Fatalf("PublishMulti order = %q, %q", first.Payload, second.Payload)
	}

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(confirmed) != 2 {
		t.Fatalf("OnSubscribed calls = %v", confirmed)
	}
	mu.Unlock()

	s.Close()
	s.Close() // idempotent
	select {
	case <-s.Done():
	default:
		t.Fatal("Done should be closed after Close")
	}
	if n, _ := pub.Publish(ctx, "ch:pod:a", []byte("after")); n != 0 {
		t.Fatalf("after Close the channel should have no subscribers, got %d", n)
	}
}

func TestPSubscribe(t *testing.T) {
	pub, sub, _ := newTestPubSub(t)
	ctx := context.Background()

	got := make(chan Message, 10)
	if _, err := sub.PSubscribe(ctx, []string{"ch:pod:*"}, func(m Message) { got <- m }); err != nil {
		t.Fatalf("PSubscribe: %v", err)
	}
	if _, err := pub.Publish(ctx, "ch:pod:xyz", []byte("hello"), WithClassicPublish()); err != nil {
		t.Fatal(err)
	}
	m := expectMessage(t, got)
	if m.Pattern != "ch:pod:*" || m.Channel != "ch:pod:xyz" || string(m.Payload) != "hello" {
		t.Fatalf("message = %+v", m)
	}
	if _, err := pub.Publish(ctx, "other", []byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		t.Fatalf("unexpected message %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSubscriptionRecoversAfterDrop(t *testing.T) {
	pub, sub, mr := newTestPubSub(t)
	ctx := context.Background()

	got := make(chan Message, 10)
	disconnected := make(chan error, 10)
	resubscribed := make(chan string, 10)
	_, err := sub.Subscribe(ctx, []string{"ch:pod:a"}, func(m Message) { got <- m },
		OnDisconnected(func(err error) { disconnected <- err }),
		OnResubscribed(func(ch string) { resubscribed <- ch }),
		WithBackoff(10*time.Millisecond, 50*time.Millisecond))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Restart drops every connection; the subscriber must notice and resubscribe.
	mr.Close()
	if err := mr.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	select {
	case err := <-disconnected:
		if err == nil {
			t.Fatal("OnDisconnected should carry the error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnDisconnected was not called")
	}
	select {
	case ch := <-resubscribed:
		if ch != "ch:pod:a" {
			t.Fatalf("OnResubscribed channel = %q", ch)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnResubscribed was not called")
	}

	// The command client reconnects on its own; publish until the message arrives.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if n, err := pub.Publish(ctx, "ch:pod:a", []byte("after-restart")); err == nil && n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("publish after restart never reached the subscriber")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if m := expectMessage(t, got); string(m.Payload) != "after-restart" {
		t.Fatalf("message = %+v", m)
	}
}

func TestSubscribeValidationAndClose(t *testing.T) {
	pub, sub, _ := newTestPubSub(t)
	ctx := context.Background()

	if _, err := sub.Subscribe(ctx, nil, func(Message) {}); err == nil {
		t.Fatal("Subscribe without channels should fail")
	}
	if _, err := sub.Subscribe(ctx, []string{"a"}, nil); err == nil {
		t.Fatal("Subscribe without a handler should fail")
	}
	if _, err := pub.Publish(ctx, " ", nil); err == nil {
		t.Fatal("Publish to a blank channel should fail")
	}

	s, err := sub.Subscribe(ctx, []string{"a"}, func(Message) {})
	if err != nil {
		t.Fatal(err)
	}
	sub.Close()
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber Close should close its subscriptions")
	}
	if _, err := sub.Subscribe(ctx, []string{"a"}, func(Message) {}); err != ErrSubscriberClosed {
		t.Fatalf("Subscribe after Close err = %v", err)
	}
}

func TestBackoffBounds(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		d := backoff(attempt, 100*time.Millisecond, time.Second)
		if d < 50*time.Millisecond || d > time.Second {
			t.Fatalf("backoff(%d) = %v out of bounds", attempt, d)
		}
	}
}

// Lifecycle callbacks run on their own goroutine: a slow one must neither stall the connection (no
// messages, no confirmations) nor block Subscribe, however many events queue up behind it.
func TestSlowCallbacksDoNotBlockTheConnection(t *testing.T) {
	pub, sub, _ := newTestPubSub(t)

	channels := make([]string, 100) // more events than any fixed buffer
	for i := range channels {
		channels[i] = fmt.Sprintf("ch:%d", i)
	}
	release := make(chan struct{})
	defer close(release)
	got := make(chan Message, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := sub.Subscribe(ctx, channels, func(m Message) { got <- m },
		OnSubscribed(func(string) { <-release }))
	if err != nil {
		t.Fatalf("Subscribe with a blocked callback: %v", err)
	}
	defer func() { go s.Close() }() // Close waits for the blocked callback

	if _, err := pub.Publish(context.Background(), "ch:99", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if m := expectMessage(t, got); string(m.Payload) != "x" {
		t.Fatalf("message = %+v", m)
	}
}

// Close must not return while a callback is still running.
func TestCloseWaitsForRunningCallback(t *testing.T) {
	_, sub, _ := newTestPubSub(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	var running atomic.Bool
	s, err := sub.Subscribe(context.Background(), []string{"ch"}, func(Message) {},
		OnSubscribed(func(string) {
			running.Store(true)
			close(entered)
			<-release
			running.Store(false)
		}))
	if err != nil {
		t.Fatal(err)
	}
	<-entered

	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while a callback was running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the callback finished")
	}
	if running.Load() {
		t.Fatal("callback still running after Close")
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Done should be closed after Close")
	}
}

// Closing the subscriber while a Subscribe is starting must not hang or leak (run with -race to
// check the ordering).
func TestSubscriberCloseRacesSubscribe(t *testing.T) {
	_, mr := testutil.NewClient(t)
	for i := 0; i < 30; i++ {
		opt := testutil.Option(mr.Addr())
		opt.DisableRetry = true
		client, err := valkeygo.NewClient(opt)
		if err != nil {
			t.Fatal(err)
		}
		sub := NewSubscriber(client, WithSubscriberLogger(testutil.DiscardLogger()))

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if s, err := sub.Subscribe(ctx, []string{"a", "b"}, func(Message) {}); err == nil {
				s.Close()
			}
		}()
		go func() { defer wg.Done(); sub.Close() }()

		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: Subscribe and Close did not both return", i)
		}
	}
}
