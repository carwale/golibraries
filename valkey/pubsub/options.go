package pubsub

import (
	"time"

	"github.com/carwale/golibraries/gologger"
)

// SubscribeOption customises a subscription.
type SubscribeOption func(*subscribeOptions)

type subscribeOptions struct {
	onSubscribed   func(channel string)
	onDisconnected func(err error)
	onResubscribed func(channel string)
	minBackoff     time.Duration
	maxBackoff     time.Duration
	sharded        *bool
}

// OnSubscribed is called once per channel (or pattern) when Valkey first confirms the subscription.
func OnSubscribed(fn func(channel string)) SubscribeOption {
	return func(o *subscribeOptions) { o.onSubscribed = fn }
}

// OnDisconnected is called each time the subscription connection drops. Messages published until
// the subscription is confirmed again are lost: pub/sub cannot replay them.
func OnDisconnected(fn func(err error)) SubscribeOption {
	return func(o *subscribeOptions) { o.onDisconnected = fn }
}

// OnResubscribed is called per channel (or pattern) when Valkey confirms it again after a drop.
// This is where a consumer triggers its catch-up (for example the chat gateway's paced resync).
func OnResubscribed(fn func(channel string)) SubscribeOption {
	return func(o *subscribeOptions) { o.onResubscribed = fn }
}

// WithBackoff sets the reconnect delay range. Delays grow exponentially from min to max, with
// jitter. Defaults: 100ms to 5s.
func WithBackoff(min, max time.Duration) SubscribeOption {
	return func(o *subscribeOptions) {
		if min > 0 {
			o.minBackoff = min
		}
		if max >= min && max > 0 {
			o.maxBackoff = max
		}
	}
}

// WithSharded overrides the subscriber default (WithShardedSubscribe, or Config.ShardedPubSub when
// created by valkey.Client) for one Subscribe call. It has no effect on
// PSubscribe, which is always classic.
func WithSharded(sharded bool) SubscribeOption {
	return func(o *subscribeOptions) { o.sharded = &sharded }
}

// PublisherOption customises the publisher returned by NewPublisher.
type PublisherOption func(*publisher)

// WithShardedPublish makes Publish use SPUBLISH (Valkey 7+, recommended in cluster mode).
func WithShardedPublish(sharded bool) PublisherOption {
	return func(p *publisher) { p.sharded = sharded }
}

// WithPublishTimeout bounds each publish when the caller's context has no deadline. Default 250ms.
func WithPublishTimeout(d time.Duration) PublisherOption {
	return func(p *publisher) {
		if d > 0 {
			p.timeout = d
		}
	}
}

// SubscriberOption customises the subscriber returned by NewSubscriber.
type SubscriberOption func(*subscriber)

// WithShardedSubscribe makes Subscribe use SSUBSCRIBE by default (Valkey 7+). A single
// subscription can still override it with WithSharded.
func WithShardedSubscribe(sharded bool) SubscriberOption {
	return func(s *subscriber) { s.sharded = sharded }
}

// WithSubscriberLogger sets the logger. Defaults to the gologger zerolog logger.
func WithSubscriberLogger(logger gologger.ILogger) SubscriberOption {
	return func(s *subscriber) {
		if logger != nil {
			s.logger = logger
		}
	}
}
