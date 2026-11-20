package pubsub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/valkey/internal/common"
	valkeygo "github.com/valkey-io/valkey-go"
)

const (
	defaultMinBackoff     = 100 * time.Millisecond
	defaultMaxBackoff     = 5 * time.Second
	defaultConfirmTimeout = 10 * time.Second
	unsubscribeTimeout    = time.Second
)

var (
	// ErrSubscriberClosed is returned when subscribing on a closed subscriber.
	ErrSubscriberClosed = errors.New("valkey: subscriber is closed")
	// ErrEmptyChannel is returned for an empty or blank channel or pattern.
	ErrEmptyChannel = errors.New("valkey: channel is empty")
	// errUnsubscribed is reported when the server ends a subscription without an error.
	errUnsubscribed = errors.New("valkey: subscription ended by the server")
)

// ISubscription is a running subscription.
type ISubscription interface {
	// Channels returns the channels or patterns of this subscription.
	Channels() []string
	// Done is closed once the subscription has fully stopped.
	Done() <-chan struct{}
	// Close unsubscribes the channels no other subscription uses and waits for a running handler
	// and callback to return.
	Close()
}

// ISubscriber creates subscriptions on a dedicated connection set.
type ISubscriber interface {
	// Subscribe subscribes to channels (SSUBSCRIBE when sharded, SUBSCRIBE otherwise) and blocks
	// until Valkey has confirmed every channel or ctx ends (10s when ctx has no deadline). The
	// subscription then keeps running, resubscribing after drops, until Close.
	//
	// Readiness is all or nothing: if any channel is not confirmed in time (an ACL deny, for
	// example) the whole subscription is closed and an error returned. Subscribe channels that can
	// fail independently in separate calls. Each channel has its own receive goroutine and subscribe
	// round trip on the subscriber, so keep the channel count small (one per pod, not one per user).
	//
	// Several subscriptions may share a channel: the channel is subscribed once on the connection and
	// each subscription gets every message; closing one leaves the others subscribed. Blank channels
	// are rejected with ErrEmptyChannel; duplicates are dropped.
	//
	// handler runs on the channel's receive goroutine, one message after another with the handlers
	// of the other subscriptions to that channel, and must not block: valkey-go buffers only a few
	// messages per channel, after which the shared connection stalls. Each channel has its own
	// goroutine, so a subscription to several channels gets concurrent handler calls: handler must
	// be safe for concurrent use, and messages are in publish order only within one channel. A
	// panic in handler is recovered and logged, and that message is dropped. Lifecycle callbacks
	// (OnSubscribed, OnDisconnected, OnResubscribed) run one at a time on a separate goroutine, so
	// they may be slow without delaying messages; a panic in one is recovered and logged too.
	Subscribe(ctx context.Context, channels []string, handler Handler, opts ...SubscribeOption) (ISubscription, error)
	// PSubscribe subscribes to glob patterns (for example "ch:pod:*") with PSUBSCRIBE. Valkey has no
	// sharded pattern subscription, so it only receives messages sent with classic PUBLISH. Whenever
	// the publisher is sharded (Config.ShardedPubSub true, cluster or standalone), publish the
	// channels it should see with WithClassicPublish. Handler concurrency and ordering are as for
	// Subscribe, per pattern.
	PSubscribe(ctx context.Context, patterns []string, handler Handler, opts ...SubscribeOption) (ISubscription, error)
	// Close closes every subscription and the subscriber's connections.
	Close()
}

type subscriber struct {
	client  valkeygo.Client
	sharded bool
	logger  gologger.ILogger

	mu     sync.Mutex
	subs   map[*subscription]struct{}
	closed bool

	// feeds holds the one receive loop per (kind, name) that every subscription to the name shares
	// (see feed). A feed is stopped and its name unsubscribed when its last subscription detaches.
	// refMu also orders a subscription start after an in-flight unsubscribe of the same name.
	// Lock order: refMu, then mu or a feed's mu, then a subscription's mu.
	refMu sync.Mutex
	feeds map[refKey]*feed
}

type refKey struct{ kind, name string }

var _ ISubscriber = (*subscriber)(nil)

// NewSubscriber returns an ISubscriber that owns client and closes it on Close. Give it a client
// used for nothing else, created with ClientOption.DisableRetry set: otherwise valkey-go retries
// dropped subscriptions internally and OnDisconnected / OnResubscribed never fire. Most services use
// valkey.Client.NewSubscriber, which does this.
func NewSubscriber(client valkeygo.Client, opts ...SubscriberOption) ISubscriber {
	s := &subscriber{client: client, subs: map[*subscription]struct{}{}, feeds: map[refKey]*feed{}}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = common.DefaultLogger()
	}
	return s
}

func (s *subscriber) Subscribe(ctx context.Context, channels []string, handler Handler, opts ...SubscribeOption) (ISubscription, error) {
	o := applySubscribeOptions(opts)
	names, err := uniqueNames(channels)
	if err != nil {
		return nil, err
	}
	return s.start(ctx, s.channelKind(o), names, handler, o)
}

// channelKind picks SSUBSCRIBE or SUBSCRIBE from the subscriber default and the WithSharded
// override.
func (s *subscriber) channelKind(o subscribeOptions) string {
	sharded := s.sharded
	if o.sharded != nil {
		sharded = *o.sharded
	}
	if sharded {
		return "ssubscribe"
	}
	return "subscribe"
}

func (s *subscriber) PSubscribe(ctx context.Context, patterns []string, handler Handler, opts ...SubscribeOption) (ISubscription, error) {
	names, err := uniqueNames(patterns)
	if err != nil {
		return nil, err
	}
	return s.start(ctx, "psubscribe", names, handler, applySubscribeOptions(opts))
}

// uniqueNames rejects blank names and drops duplicates, keeping order.
func uniqueNames(names []string) ([]string, error) {
	for _, n := range names {
		if common.Blank(n) {
			return nil, ErrEmptyChannel
		}
	}
	return common.UniqueKeys(names), nil
}

func applySubscribeOptions(opts []SubscribeOption) subscribeOptions {
	o := subscribeOptions{minBackoff: defaultMinBackoff, maxBackoff: defaultMaxBackoff}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func (s *subscriber) start(ctx context.Context, kind string, names []string, handler Handler, o subscribeOptions) (ISubscription, error) {
	if len(names) == 0 {
		return nil, errors.New("valkey: no channels or patterns to subscribe to")
	}
	if handler == nil {
		return nil, errors.New("valkey: subscribe needs a handler")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.refMu.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.refMu.Unlock()
		return nil, ErrSubscriberClosed
	}
	sub := newSubscription(s, kind, names, handler, o)
	sub.dispatchW.Add(1)
	go sub.dispatch()
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	// Attach before releasing refMu: a concurrent Close then sees every feed it must detach from.
	for _, n := range names {
		key := refKey{kind, n}
		f := s.feeds[key]
		if f == nil {
			f = newFeed(s, key)
			s.feeds[key] = f
			go f.run()
		}
		sub.feeds = append(sub.feeds, f)
		f.attach(sub)
	}
	s.refMu.Unlock()

	wait, cancel := common.WithTimeout(ctx, defaultConfirmTimeout)
	defer cancel()
	select {
	case <-sub.ready:
		return sub, nil
	case <-sub.done:
		// The subscriber was closed while this subscription was starting.
		return nil, ErrSubscriberClosed
	case <-sub.failed:
		sub.Close()
		return nil, fmt.Errorf("valkey: subscribing to %s: %w", strings.Join(names, ", "), sub.lastError())
	case <-wait.Done():
		sub.Close()
		what := strings.Join(names, ", ")
		if last := sub.lastError(); last != nil {
			return nil, fmt.Errorf("valkey: waiting for subscription to %s: %w (last error: %w)", what, wait.Err(), last)
		}
		return nil, fmt.Errorf("valkey: waiting for subscription to %s: %w", what, wait.Err())
	}
}

// release detaches sub from its feeds. A feed left without subscriptions is stopped and its name
// unsubscribed, one name per command like the subscribe side (SUNSUBSCRIBE names must share a slot
// anyway). It holds refMu throughout, so a subscription to the same name that starts concurrently
// sends its SUBSCRIBE only after the UNSUBSCRIBE has completed.
func (s *subscriber) release(sub *subscription) {
	s.refMu.Lock()
	defer s.refMu.Unlock()
	for _, f := range sub.feeds {
		if f.detach(sub) > 0 {
			continue
		}
		delete(s.feeds, f.key)
		// Stop the local Receive first, then tell the server: a Receive's registration is removed
		// as it returns, so nothing is buffered for it in between.
		f.stop()
		ctx, cancel := context.WithTimeout(context.Background(), unsubscribeTimeout)
		_ = s.client.Do(ctx, buildUnsubscribe(s.client.B(), f.key.kind, []string{f.key.name})).Error()
		cancel()
	}
}

func (s *subscriber) remove(sub *subscription) {
	s.mu.Lock()
	delete(s.subs, sub)
	s.mu.Unlock()
}

func (s *subscriber) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	subs := make([]*subscription, 0, len(s.subs))
	for sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()

	for _, sub := range subs {
		sub.Close()
	}
	s.client.Close()
}
