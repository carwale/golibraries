package pubsub

import (
	"context"
	"time"

	"github.com/carwale/golibraries/valkey/internal/common"
	valkeygo "github.com/valkey-io/valkey-go"
)

// PublishOption customises one publish call.
type PublishOption func(*publishOptions)

type publishOptions struct {
	classic bool
}

// WithClassicPublish forces PUBLISH even when the client is configured for sharded pub/sub. Use it
// for channels observed with PSubscribe: pattern subscribers never see SPUBLISH messages.
func WithClassicPublish() PublishOption {
	return func(o *publishOptions) { o.classic = true }
}

// IPublisher publishes pub/sub messages.
type IPublisher interface {
	// Publish sends payload to channel and returns the number of subscribers that received it
	// (for SPUBLISH, the subscribers on that shard). It uses SPUBLISH when sharded pub/sub is
	// configured, PUBLISH otherwise.
	Publish(ctx context.Context, channel string, payload []byte, opts ...PublishOption) (int64, error)
	// PublishMulti pipelines many publishes in one round trip per node. The returned slice has one
	// error (nil on success) per message, in order.
	PublishMulti(ctx context.Context, msgs []OutboundMessage, opts ...PublishOption) []error
}

type publisher struct {
	client  valkeygo.Client
	timeout time.Duration
	sharded bool
}

var _ IPublisher = (*publisher)(nil)

// NewPublisher returns an IPublisher on an existing valkey-go client. Most services get it from
// valkey.Client.Publisher instead.
func NewPublisher(client valkeygo.Client, opts ...PublisherOption) IPublisher {
	p := &publisher{client: client, timeout: common.DefaultRequestTimeout}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *publisher) cmd(channel string, payload []byte, o publishOptions) valkeygo.Completed {
	msg := valkeygo.BinaryString(payload)
	if p.sharded && !o.classic {
		return p.client.B().Spublish().Channel(channel).Message(msg).Build()
	}
	return p.client.B().Publish().Channel(channel).Message(msg).Build()
}

func applyPublishOptions(opts []PublishOption) publishOptions {
	var o publishOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func (p *publisher) Publish(ctx context.Context, channel string, payload []byte, opts ...PublishOption) (int64, error) {
	if common.Blank(channel) {
		return 0, ErrEmptyChannel
	}
	ctx, cancel := common.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.client.Do(ctx, p.cmd(channel, payload, applyPublishOptions(opts))).AsInt64()
}

func (p *publisher) PublishMulti(ctx context.Context, msgs []OutboundMessage, opts ...PublishOption) []error {
	errs := make([]error, len(msgs))
	if len(msgs) == 0 {
		return errs
	}
	o := applyPublishOptions(opts)

	// Blank channels are rejected locally; the rest go out in one pipeline.
	cmds := make(valkeygo.Commands, 0, len(msgs))
	index := make([]int, 0, len(msgs))
	for i, m := range msgs {
		if common.Blank(m.Channel) {
			errs[i] = ErrEmptyChannel
			continue
		}
		cmds = append(cmds, p.cmd(m.Channel, m.Payload, o))
		index = append(index, i)
	}
	if len(cmds) == 0 {
		return errs
	}
	ctx, cancel := common.WithTimeout(ctx, p.timeout)
	defer cancel()
	for j, r := range p.client.DoMulti(ctx, cmds...) {
		errs[index[j]] = r.Error()
	}
	return errs
}
