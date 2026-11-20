package pubsub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"
)

// feed is the one valkey-go Receive for a (kind, name) on the subscriber's connection. Every
// subscription to that name attaches to the same feed, which fans messages and confirmations out to
// them.
//
// There must never be two Receive calls covering one name on a connection: valkey-go registers a
// Receive's 16-message buffer before it sends SUBSCRIBE and does not read it until its subscribe
// call returns. With several Receives (and their SUBSCRIBE/UNSUBSCRIBE commands) covering a busy
// channel, the connection's reader was seen blocked on such a full buffer while its owner still
// waited for its reply, which stalled every subscription on the connection for good
// (TestChurnOnSharedChannelsDoesNotStall). With one feed per name and one name per command, a
// name's messages only start after its own Receive's SUBSCRIBE.
type feed struct {
	owner *subscriber
	key   refKey

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// subs is read for every message without a lock; writes copy it under mu.
	subs atomic.Pointer[[]*subscription]
	// mu orders attach and detach with confirmations and drops, so each subscription sees a name's
	// events in order and an attaching subscription never misses the current state.
	mu sync.Mutex
	up bool // the server has confirmed the subscription since the last drop
}

func newFeed(owner *subscriber, key refKey) *feed {
	ctx, cancel := context.WithCancel(context.Background())
	f := &feed{owner: owner, key: key, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	f.subs.Store(&[]*subscription{})
	return f
}

// attach adds sub. If the name is already confirmed, sub is confirmed at once.
func (f *feed) attach(sub *subscription) {
	f.mu.Lock()
	defer f.mu.Unlock()
	old := *f.subs.Load()
	next := make([]*subscription, len(old), len(old)+1)
	copy(next, old)
	next = append(next, sub)
	f.subs.Store(&next)
	if f.up {
		sub.confirm(f.key.name)
	}
}

// detach removes sub and returns how many subscriptions remain.
func (f *feed) detach(sub *subscription) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	old := *f.subs.Load()
	next := make([]*subscription, 0, len(old))
	for _, s := range old {
		if s != sub {
			next = append(next, s)
		}
	}
	f.subs.Store(&next)
	return len(next)
}

// stop ends the receive loop and waits for it. Detached subscriptions get no more calls from it.
func (f *feed) stop() {
	f.cancel()
	<-f.done
}

func (f *feed) build(b valkeygo.Builder) valkeygo.Completed {
	switch f.key.kind {
	case "ssubscribe":
		return b.Ssubscribe().Channel(f.key.name).Build()
	case "psubscribe":
		return b.Psubscribe().Pattern(f.key.name).Build()
	default:
		return b.Subscribe().Channel(f.key.name).Build()
	}
}

// deliver runs on the connection's receive path: it hands m to every attached subscription.
func (f *feed) deliver(m valkeygo.PubSubMessage) {
	for _, s := range *f.subs.Load() {
		s.deliver(m)
	}
}

// confirmed runs on valkey-go's connection goroutine for this name's subscribe confirmation. It
// must not block.
func (f *feed) confirmed() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.up = true
	for _, s := range *f.subs.Load() {
		s.confirm(f.key.name)
	}
}

// dropped records a failed or ended Receive and reports it to every attached subscription. It
// returns whether the subscription had been confirmed before the drop.
func (f *feed) dropped(err error) (wasUp bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wasUp, f.up = f.up, false
	for _, s := range *f.subs.Load() {
		s.dropped(f.key.name, err)
	}
	return wasUp
}

// backoffRange is the tightest range any attached subscription asked for.
func (f *feed) backoffRange() (min, max time.Duration) {
	min, max = defaultMinBackoff, defaultMaxBackoff
	for i, s := range *f.subs.Load() {
		if i == 0 || s.opts.minBackoff < min {
			min = s.opts.minBackoff
		}
		if i == 0 || s.opts.maxBackoff < max {
			max = s.opts.maxBackoff
		}
	}
	return min, max
}

// run subscribes and resubscribes after every drop until stop.
func (f *feed) run() {
	defer close(f.done)
	client := f.owner.client
	attempt := 0
	for {
		hookCtx := valkeygo.WithOnSubscriptionHook(f.ctx, func(ps valkeygo.PubSubSubscription) {
			if ps.Kind == f.key.kind && ps.Channel == f.key.name {
				f.confirmed()
			}
		})
		err := client.Receive(hookCtx, f.build(client.B()), f.deliver)
		if f.ctx.Err() != nil || errors.Is(err, valkeygo.ErrClosing) {
			return
		}
		if err == nil {
			err = errUnsubscribed
		}
		if f.dropped(err) {
			attempt = 0 // the last attempt worked for a while; restart the backoff
		}
		f.owner.logger.LogError("valkey: subscription to "+f.key.name+" dropped, resubscribing", err)

		min, max := f.backoffRange()
		select {
		case <-f.ctx.Done():
			return
		case <-time.After(backoff(attempt, min, max)):
		}
		attempt++
	}
}
