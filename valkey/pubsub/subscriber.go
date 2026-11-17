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
	// handler runs on the connection goroutine, in order, and must not block. Lifecycle callbacks
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
}

var _ ISubscriber = (*subscriber)(nil)

// NewSubscriber returns an ISubscriber that owns client and closes it on Close. Give it a client
// used for nothing else, created with ClientOption.DisableRetry set: otherwise valkey-go retries
// dropped subscriptions internally and OnDisconnected / OnResubscribed never fire. Most services use
// valkey.Client.NewSubscriber, which does this.
func NewSubscriber(client valkeygo.Client, opts ...SubscriberOption) ISubscriber {
	s := &subscriber{client: client, subs: map[*subscription]struct{}{}}
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
	sharded := s.sharded
	if o.sharded != nil {
		sharded = *o.sharded
	}
	names := common.UniqueKeys(channels)
	var loops []*receiveLoop
	if sharded {
		// SSUBSCRIBE channels must share a slot, so each channel gets its own loop.
		for _, ch := range names {
			loops = append(loops, &receiveLoop{kind: "ssubscribe", names: []string{ch}})
		}
	} else if len(names) > 0 {
		loops = []*receiveLoop{{kind: "subscribe", names: names}}
	}
	return s.start(ctx, names, loops, handler, o)
}

func (s *subscriber) PSubscribe(ctx context.Context, patterns []string, handler Handler, opts ...SubscribeOption) (ISubscription, error) {
	names := common.UniqueKeys(patterns)
	var loops []*receiveLoop
	if len(names) > 0 {
		loops = []*receiveLoop{{kind: "psubscribe", names: names}}
	}
	return s.start(ctx, names, loops, handler, applySubscribeOptions(opts))
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

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSubscriberClosed
	}
	sub := newSubscription(s, names, handler, o)
	// Start it before releasing the lock: a concurrent Close then sees it fully started.
	sub.run(loops)
	s.subs[sub] = struct{}{}
	s.mu.Unlock()

	wait, cancel := common.WithTimeout(ctx, defaultConfirmTimeout)
	defer cancel()
	select {
	case <-sub.ready:
		return sub, nil
	case <-sub.done:
		// The subscriber was closed while this subscription was starting.
		return nil, ErrSubscriberClosed
	case <-wait.Done():
		sub.Close()
		return nil, fmt.Errorf("valkey: waiting for subscription to %s: %w", strings.Join(names, ", "), wait.Err())
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
