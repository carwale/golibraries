package pubsub

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
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
	slowStopWarning       = 5 * time.Second
	defaultCloseTimeout   = 10 * time.Second
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
	// Close closes every subscription and the subscriber's connections. It waits up to 10s for
	// running handlers and callbacks to return; past that it logs the channels still waiting and
	// closes the connections anyway. A concurrent Close returns once the first has finished.
	Close()
}

type subscriber struct {
	client  valkeygo.Client
	sharded bool
	logger  gologger.ILogger

	mu     sync.Mutex
	subs   map[*subscription]struct{}
	closed bool
	// closeDone is closed when Close has finished; a later Close waits for it.
	closeDone    chan struct{}
	closeTimeout time.Duration

	// feeds holds the one receive loop per (kind, name) that every subscription to the name shares
	// (see feed). A feed is stopped and its name unsubscribed when its last subscription detaches.
	// stopping holds the names whose last feed is still stopping or unsubscribing; it is closed when
	// that is done, and a new subscription to such a name waits for it first, so two receive loops
	// never cover one name. refMu guards both maps and is never held while waiting on a feed.
	// Lock order: refMu, then mu or a feed's mu, then a subscription's mu.
	refMu    sync.Mutex
	feeds    map[refKey]*feed
	stopping map[refKey]chan struct{}
}

type refKey struct{ kind, name string }

var _ ISubscriber = (*subscriber)(nil)

// NewSubscriber returns an ISubscriber that owns client and closes it on Close. Give it a client
// used for nothing else, created with ClientOption.DisableRetry set: otherwise valkey-go retries
// dropped subscriptions internally and OnDisconnected / OnResubscribed never fire. Most services use
// valkey.Client.NewSubscriber, which does this.
func NewSubscriber(client valkeygo.Client, opts ...SubscriberOption) ISubscriber {
	s := &subscriber{
		client:       client,
		subs:         map[*subscription]struct{}{},
		closeDone:    make(chan struct{}),
		closeTimeout: defaultCloseTimeout,
		feeds:        map[refKey]*feed{},
		stopping:     map[refKey]chan struct{}{},
	}
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
	wait, cancel := common.WithTimeout(ctx, defaultConfirmTimeout)
	defer cancel()

	// Wait out an earlier subscription's unsubscribe of any of these names. The wait is outside
	// refMu, so a slow one (a handler that has not returned) holds up only subscriptions to its own
	// names, and only until ctx ends.
	for {
		s.refMu.Lock()
		busy := s.stoppingAny(kind, names)
		if busy == nil {
			break // refMu stays held
		}
		s.refMu.Unlock()
		select {
		case <-busy:
		case <-wait.Done():
			return nil, fmt.Errorf("valkey: waiting for an earlier subscription to %s to close: %w", strings.Join(names, ", "), wait.Err())
		}
	}
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

// stoppingAny returns the stop signal of the first of names still being unsubscribed, or nil.
// Callers hold refMu.
func (s *subscriber) stoppingAny(kind string, names []string) chan struct{} {
	for _, n := range names {
		if ch := s.stopping[refKey{kind, n}]; ch != nil {
			return ch
		}
	}
	return nil
}

// release detaches sub from its feeds. A feed left without subscriptions is stopped and its name
// unsubscribed, one name per command like the subscribe side (SUNSUBSCRIBE names must share a slot
// anyway). Waiting for a feed to stop means waiting for a running handler, so it happens outside
// refMu: a stuck handler blocks this Close, not the subscriber. The name stays in s.stopping until
// the UNSUBSCRIBE has completed, so a new subscription to it sends its SUBSCRIBE only after that.
func (s *subscriber) release(sub *subscription) {
	var last []*feed
	s.refMu.Lock()
	for _, f := range sub.feeds {
		if f.detach(sub) > 0 {
			continue
		}
		delete(s.feeds, f.key)
		s.stopping[f.key] = f.released
		last = append(last, f)
	}
	s.refMu.Unlock()

	for _, f := range last {
		// Stop the local Receive first, then tell the server: a Receive's registration is removed
		// as it returns, so nothing is buffered for it in between.
		s.stopFeed(f)
		if node := f.node; node != nil {
			// The node the Receive ran on: a classic or pattern UNSUBSCRIBE sent through a cluster
			// client would go to an arbitrary node and could miss the subscription.
			ctx, cancel := context.WithTimeout(context.Background(), unsubscribeTimeout)
			_ = node.Do(ctx, buildUnsubscribe(node.B(), f.key.kind, []string{f.key.name})).Error()
			cancel()
		}
		s.refMu.Lock()
		delete(s.stopping, f.key)
		s.refMu.Unlock()
		close(f.released)
	}
}

// stopFeed stops f, logging if a handler keeps it from stopping for long.
func (s *subscriber) stopFeed(f *feed) {
	slow := time.AfterFunc(slowStopWarning, func() {
		s.logger.LogErrorWithoutErrorf("valkey: closing the subscription to %s has waited %v for a handler to return; new subscriptions to it wait too", f.key.name, slowStopWarning)
	})
	f.stop()
	slow.Stop()
}

// receiver returns the client a feed's Receive runs on, and later its unsubscribe. Sharded commands
// carry a slot and go through the client, which routes them to the slot's primary. Classic and
// pattern commands carry none, and a cluster client sends each one to an arbitrary node, so they
// are pinned to one node per attempt; any node works, since PUBLISH reaches every node.
func (s *subscriber) receiver(kind string) valkeygo.Client {
	if kind == "ssubscribe" {
		return s.client
	}
	nodes := s.client.Nodes()
	if len(nodes) == 0 {
		return s.client
	}
	addrs := make([]string, 0, len(nodes))
	for addr := range nodes {
		addrs = append(addrs, addr)
	}
	return nodes[addrs[rand.Intn(len(addrs))]] //nolint:gosec // spreading load, not security
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
		<-s.closeDone
		return
	}
	s.closed = true
	subs := make([]*subscription, 0, len(s.subs))
	for sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()
	defer close(s.closeDone)

	// In parallel: each Close waits for its own running handler and callback, and one slow one
	// should not delay the others.
	var wg sync.WaitGroup
	for _, sub := range subs {
		wg.Add(1)
		go func(sub *subscription) {
			defer wg.Done()
			sub.Close()
		}(sub)
	}
	all := make(chan struct{})
	go func() {
		wg.Wait()
		close(all)
	}()
	timer := time.NewTimer(s.closeTimeout)
	defer timer.Stop()
	select {
	case <-all:
	case <-timer.C:
		// A handler or callback has not returned. Shutdown must not hang on it: close the
		// connections anyway. Its subscription finishes closing once it returns.
		var stuck []string
		for _, sub := range subs {
			select {
			case <-sub.done:
			default:
				stuck = append(stuck, sub.names...)
			}
		}
		s.logger.LogErrorWithoutErrorf("valkey: subscriber Close waited %v for a handler or callback on %s to return; closing the connections anyway", s.closeTimeout, strings.Join(stuck, ", "))
	}
	s.client.Close()
}
