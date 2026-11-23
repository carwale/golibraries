package pubsub

import (
	"context"
	"errors"
	"fmt"
	"github.com/alicebob/miniredis/v2"
	"strings"
	"sync"
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

// A panicking lifecycle callback must not kill the process; later callbacks and messages still run.
func TestCallbackPanicIsRecovered(t *testing.T) {
	client, mr := testutil.NewClient(t)
	opt := testutil.Option(mr.Addr())
	opt.DisableRetry = true
	logger, logs := testutil.BufferLogger()
	sub := NewSubscriber(testutil.Connect(t, opt), WithSubscriberLogger(logger))
	t.Cleanup(sub.Close)
	pub := NewPublisher(client)
	ctx := context.Background()

	got := make(chan Message, 10)
	subscribed := make(chan string, 2)
	if _, err := sub.Subscribe(ctx, []string{"a", "b"}, func(m Message) { got <- m },
		OnSubscribed(func(ch string) {
			subscribed <- ch
			if ch == "a" {
				panic("bad callback")
			}
		})); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-subscribed:
		case <-time.After(2 * time.Second):
			t.Fatal("OnSubscribed did not run for both channels")
		}
	}
	if _, err := pub.Publish(ctx, "a", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	expectMessage(t, got)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.String(), "lifecycle callback panicked") {
		if time.Now().After(deadline) {
			t.Fatalf("callback panic not logged; logs: %s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
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
	kind := s.channelKind(subscribeOptions{})
	if got := cmd(newFeed(s, refKey{kind, "a"}).build(client.B())); got != "SSUBSCRIBE" {
		t.Fatalf("sharded subscribe = %s", got)
	}
	if got := cmd(buildUnsubscribe(client.B(), kind, []string{"a"})); got != "SUNSUBSCRIBE" {
		t.Fatalf("sharded unsubscribe = %s", got)
	}
	off := false
	kind = s.channelKind(subscribeOptions{sharded: &off})
	if got := cmd(newFeed(s, refKey{kind, "a"}).build(client.B())); got != "SUBSCRIBE" {
		t.Fatalf("WithSharded(false) subscribe = %s", got)
	}
	if got := cmd(newFeed(s, refKey{"psubscribe", "p:*"}).build(client.B())); got != "PSUBSCRIBE" {
		t.Fatalf("pattern subscribe = %s", got)
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

// Subscriptions sharing channels with a long-lived one, opened and closed concurrently while
// messages flow, must neither stall the connection nor cost the long-lived one a message. Each
// valkey-go Receive registers a 16-message buffer it does not read until its SUBSCRIBE is
// confirmed; when two Receives covered one channel on the shared connection, messages for that
// channel could fill the buffer first, block the connection's reader and deadlock every
// subscription. Now one feed per channel serves every subscription to it.
func TestChurnOnSharedChannelsDoesNotStall(t *testing.T) {
	pub, sub, _ := newTestPubSub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var steady atomic.Int64
	if _, err := sub.Subscribe(ctx, []string{"churn:a"}, func(Message) { steady.Add(1) }); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var publishedA atomic.Int64
	pubDone := make(chan error, 1)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				pubDone <- nil
				return
			default:
			}
			ch := "churn:b"
			if i%2 == 0 {
				ch = "churn:a"
			}
			if _, err := pub.Publish(ctx, ch, []byte("x")); err != nil {
				pubDone <- err
				return
			}
			if ch == "churn:a" {
				publishedA.Add(1)
			}
		}
	}()

	sets := [][]string{{"churn:a"}, {"churn:b"}, {"churn:a", "churn:b"}}
	const churners, rounds = 10, 30
	errs := make(chan error, churners)
	for w := 0; w < churners; w++ {
		go func(w int) {
			for i := 0; i < rounds; i++ {
				s, err := sub.Subscribe(ctx, sets[(w+i)%len(sets)], func(Message) {})
				if err != nil {
					errs <- fmt.Errorf("churner %d, Subscribe #%d: %w", w, i, err)
					return
				}
				s.Close()
			}
			errs <- nil
		}(w)
	}
	for w := 0; w < churners; w++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("subscribe/close on shared channels stalled the connection")
		}
	}
	close(stop)
	if err := <-pubDone; err != nil {
		t.Fatalf("Publish stalled: %v", err)
	}
	want := publishedA.Load()
	deadline := time.Now().Add(5 * time.Second)
	for steady.Load() < want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := steady.Load(); n != want {
		t.Fatalf("long-lived subscription got %d of %d messages", n, want)
	}
}

// Each channel has its own receive loop, but a connection drop is still one outage per
// subscription: one OnDisconnected, then one OnResubscribed per channel.
func TestMultiChannelSubscriptionReportsOneOutage(t *testing.T) {
	_, sub, mr := newTestPubSub(t)

	var disconnects atomic.Int32
	resubscribed := make(chan string, 10)
	if _, err := sub.Subscribe(context.Background(), []string{"a", "b", "c"}, func(Message) {},
		OnDisconnected(func(error) { disconnects.Add(1) }),
		OnResubscribed(func(ch string) { resubscribed <- ch }),
		WithBackoff(10*time.Millisecond, 20*time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	mr.Close()
	time.Sleep(200 * time.Millisecond)
	if err := mr.Restart(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for len(seen) < 3 {
		select {
		case ch := <-resubscribed:
			if seen[ch] {
				t.Fatalf("OnResubscribed(%q) twice for one outage", ch)
			}
			seen[ch] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("OnResubscribed for %v only", seen)
		}
	}
	if n := disconnects.Load(); n != 1 {
		t.Fatalf("OnDisconnected called %d times for one outage of a 3-channel subscription, want 1", n)
	}
}

// Every outage is reported: after the subscription has fully recovered from one, the next drop is a
// new OnDisconnected.
func TestOnDisconnectedOnceForEachOutage(t *testing.T) {
	_, sub, mr := newTestPubSub(t)

	disconnected := make(chan error, 10)
	resubscribed := make(chan string, 10)
	if _, err := sub.Subscribe(context.Background(), []string{"ch"}, func(Message) {},
		OnDisconnected(func(err error) { disconnected <- err }),
		OnResubscribed(func(ch string) { resubscribed <- ch }),
		WithBackoff(10*time.Millisecond, 20*time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	for outage := 1; outage <= 2; outage++ {
		mr.Close()
		time.Sleep(100 * time.Millisecond) // a few failed reconnect attempts
		if err := mr.Restart(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-disconnected:
		case <-time.After(5 * time.Second):
			t.Fatalf("OnDisconnected was not called for outage %d", outage)
		}
		select {
		case <-resubscribed:
		case <-time.After(5 * time.Second):
			t.Fatalf("OnResubscribed was not called after outage %d", outage)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(disconnected); n != 0 {
		t.Fatalf("%d extra OnDisconnected calls for two outages", n)
	}
}

// A handler that never returns blocks its own Close and new subscriptions to its channel (until
// their ctx ends), but closing it does not hold up Subscribe or Close on other channels. The channel
// stays quiet here: once valkey-go's 16-message buffer for it fills, the whole connection stalls.
func TestStuckHandlerDoesNotBlockOtherChannelsClose(t *testing.T) {
	pub, sub, _ := newTestPubSub(t)
	ctx := context.Background()

	entered := make(chan struct{}, 1)
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	release := func() { unblockOnce.Do(func() { close(unblock) }) }
	t.Cleanup(release) // runs before the subscriber's Close, so a failure cannot hang the test
	stuck, err := sub.Subscribe(ctx, []string{"stuck"}, func(Message) {
		entered <- struct{}{}
		<-unblock
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := sub.Subscribe(ctx, []string{"other"}, func(Message) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pub.Publish(ctx, "stuck", []byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not called")
	}

	closed := make(chan struct{})
	go func() {
		stuck.Close()
		close(closed)
	}()
	time.Sleep(50 * time.Millisecond) // let Close reach the wait for the handler

	done := make(chan error, 1)
	go func() {
		s, err := sub.Subscribe(ctx, []string{"fresh"}, func(Message) {})
		if err == nil {
			s.Close()
			other.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Subscribe on another channel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe and Close on other channels blocked behind a stuck handler")
	}

	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := sub.Subscribe(short, []string{"stuck"}, func(Message) {}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Subscribe to the closing channel = %v, want context.DeadlineExceeded", err)
	}
	select {
	case <-closed:
		t.Fatal("Close returned while its handler was still running")
	default:
	}

	release()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the handler did")
	}
	again := make(chan Message, 1)
	if _, err := sub.Subscribe(ctx, []string{"stuck"}, func(m Message) { again <- m }); err != nil {
		t.Fatalf("Subscribe after Close: %v", err)
	}
	if _, err := pub.Publish(ctx, "stuck", []byte("y")); err != nil {
		t.Fatal(err)
	}
	if m := expectMessage(t, again); string(m.Payload) != "y" {
		t.Fatalf("message = %+v", m)
	}
}

// Shutdown must not hang on a handler that never returns: subscriber Close waits up to its timeout,
// logs the stuck channel and closes the connections anyway. A concurrent Close waits for the first.
func TestSubscriberCloseGivesUpOnStuckHandler(t *testing.T) {
	client, mr := testutil.NewClient(t)
	pub := NewPublisher(client)
	opt := testutil.Option(mr.Addr())
	opt.DisableRetry = true
	subClient, err := valkeygo.NewClient(opt)
	if err != nil {
		t.Fatal(err)
	}
	logger, logs := testutil.BufferLogger()
	sub := NewSubscriber(subClient, WithSubscriberLogger(logger))
	sub.(*subscriber).closeTimeout = 300 * time.Millisecond

	entered := make(chan struct{}, 1)
	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) })
	ctx := context.Background()
	if _, err := sub.Subscribe(ctx, []string{"stuck"}, func(Message) {
		entered <- struct{}{}
		<-unblock
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Subscribe(ctx, []string{"fine"}, func(Message) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := pub.Publish(ctx, "stuck", []byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not called")
	}

	start := time.Now()
	first, second := make(chan time.Duration, 1), make(chan time.Duration, 1)
	go func() {
		sub.Close()
		first <- time.Since(start)
	}()
	time.Sleep(50 * time.Millisecond)
	go func() {
		sub.Close()
		second <- time.Since(start)
	}()

	var tookFirst, tookSecond time.Duration
	for _, c := range []struct {
		ch   chan time.Duration
		took *time.Duration
	}{{first, &tookFirst}, {second, &tookSecond}} {
		select {
		case *c.took = <-c.ch:
		case <-time.After(3 * time.Second):
			t.Fatal("subscriber Close hung on a stuck handler")
		}
	}
	if tookFirst < 250*time.Millisecond {
		t.Fatalf("Close returned after %v, before its 300ms timeout", tookFirst)
	}
	if tookSecond < 250*time.Millisecond {
		t.Fatalf("a concurrent Close returned after %v, before the first had finished", tookSecond)
	}
	if out := logs.String(); !strings.Contains(out, "stuck") || strings.Contains(out, "fine") {
		t.Fatalf("Close should name only the stuck channel, logged: %s", out)
	}
	if err := subClient.Do(ctx, subClient.B().Ping().Build()).Error(); !errors.Is(err, valkeygo.ErrClosing) {
		t.Fatalf("PING after Close = %v, want the client closed", err)
	}
}
