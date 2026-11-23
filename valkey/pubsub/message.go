// Package pubsub publishes and subscribes to Valkey pub/sub channels: classic (PUBLISH/SUBSCRIBE),
// sharded (SPUBLISH/SSUBSCRIBE, Valkey 7+) and pattern (PSUBSCRIBE) subscriptions, with automatic
// resubscription and lifecycle callbacks.
//
// Pub/sub is at-most-once: messages published while a subscription is down are lost. Use
// OnDisconnected and OnResubscribed to know when to catch up from the source of truth.
package pubsub

// Message is one received pub/sub message.
type Message struct {
	// Channel is the channel the message was published to.
	Channel string
	// Pattern is the matching pattern, set only for PSubscribe.
	Pattern string
	// Payload is the message body.
	Payload []byte
}

// Handler processes a received message. Each channel (or pattern) has its own receive goroutine, so
// the handler of a subscription to several channels is called concurrently and must be safe for
// concurrent use; messages arrive in publish order only within one channel. It must not block:
// hand slow work to another goroutine.
type Handler func(Message)

// OutboundMessage is one message for PublishMulti.
type OutboundMessage struct {
	Channel string
	Payload []byte
}
