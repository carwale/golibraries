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
	// Close unsubscribes and waits for the receive loops and callbacks to stop.
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
	// fail independently in separate calls. When sharded, each channel gets its own receive loop and
	// subscribe round trip, so keep the channel count per subscription small (one per pod, not one
	// per user).
	//
	// Several subscriptions may share a channel: each gets every message, and closing one leaves the
	// others subscribed. Blank channels are rejected with ErrEmptyChannel; duplicates are dropped.
	//
	// handler runs on the subscription's receive goroutine, in order, and must not block: valkey-go
	// buffers only a few messages per receiver, after which the shared connection stalls. A panic in
	// handler is recovered and logged, and that message is dropped. Lifecycle callbacks
	// (OnSubscribed, OnDisconnected, OnResubscribed) run one at a time on a separate goroutine, so
	// they may be slow without delaying messages.
	Subscribe(ctx context.Context, channels []string, handler Handler, opts ...SubscribeOption) (ISubscription, error)
	// PSubscribe subscribes to glob patterns (for example "ch:pod:*") with PSUBSCRIBE. Valkey has no
	// sharded pattern subscription, so it only receives messages sent with classic PUBLISH; in a
	// cluster, publish those with WithClassicPublish.
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

	// refs counts the open subscriptions per (kind, name). The connection is shared and an
	// UNSUBSCRIBE ends every receiver of that name on it, so it is sent only when the count reaches
	// 0. refMu also orders a subscription start after an in-flight unsubscribe of the same name.
	// Lock order: refMu before mu.
	refMu sync.Mutex
	refs  map[refKey]int
}

type refKey struct{ kind, name string }

var _ ISubscriber = (*subscriber)(nil)

// NewSubscriber returns an ISubscriber that owns client and closes it on Close. Give it a client
// used for nothing else, created with ClientOption.DisableRetry set: otherwise valkey-go retries
// dropped subscriptions internally and OnDisconnected / OnResubscribed never fire. Most services use
// valkey.Client.NewSubscriber, which does this.
func NewSubscriber(client valkeygo.Client, opts ...SubscriberOption) ISubscriber {
	s := &subscriber{client: client, subs: map[*subscription]struct{}{}, refs: map[refKey]int{}}
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
	return s.start(ctx, names, s.channelLoops(names, o), handler, o)
}

// channelLoops picks SSUBSCRIBE or SUBSCRIBE from the subscriber default and the WithSharded
// override.
func (s *subscriber) channelLoops(names []string, o subscribeOptions) []*receiveLoop {
	sharded := s.sharded
	if o.sharded != nil {
		sharded = *o.sharded
	}
	if len(names) == 0 {
		return nil
	}
	if !sharded {
		return []*receiveLoop{{kind: "subscribe", names: names}}
	}
	// SSUBSCRIBE channels must share a slot, so each channel gets its own loop.
	loops := make([]*receiveLoop, 0, len(names))
	for _, ch := range names {
		loops = append(loops, &receiveLoop{kind: "ssubscribe", names: []string{ch}})
	}
	return loops
}

func (s *subscriber) PSubscribe(ctx context.Context, patterns []string, handler Handler, opts ...SubscribeOption) (ISubscription, error) {
	names, err := uniqueNames(patterns)
	if err != nil {
		return nil, err
	}
	var loops []*receiveLoop
	if len(names) > 0 {
		loops = []*receiveLoop{{kind: "psubscribe", names: names}}
	}
	return s.start(ctx, names, loops, handler, applySubscribeOptions(opts))
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

func (s *subscriber) start(ctx context.Context, names []string, loops []*receiveLoop, handler Handler, o subscribeOptions) (ISubscription, error) {
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
	for _, l := range loops {
		for _, n := range l.names {
			s.refs[refKey{l.kind, n}]++
		}
	}
	sub := newSubscription(s, names, handler, o)
	// Start it before releasing the lock: a concurrent Close then sees it fully started.
	sub.run(loops)
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
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

// release drops one reference to each name of loops and unsubscribes the names no other
// subscription still uses. It holds refMu while unsubscribing, so a subscription to the same name
// that starts concurrently sends its SUBSCRIBE only after the UNSUBSCRIBE has completed.
func (s *subscriber) release(loops []*receiveLoop) {
	s.refMu.Lock()
	defer s.refMu.Unlock()
	for _, l := range loops {
		var unused []string
		for _, n := range l.names {
			k := refKey{l.kind, n}
			s.refs[k]--
			if s.refs[k] <= 0 {
				delete(s.refs, k)
				unused = append(unused, n)
			}
		}
		if len(unused) == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), unsubscribeTimeout)
		_ = s.client.Do(ctx, buildUnsubscribe(s.client.B(), l.kind, unused)).Error()
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
