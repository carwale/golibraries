package valkey

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/valkey/cache"
	"github.com/carwale/golibraries/valkey/internal/common"
	"github.com/carwale/golibraries/valkey/internal/iam"
	"github.com/carwale/golibraries/valkey/pubsub"
	"github.com/carwale/golibraries/valkey/store"
	"github.com/carwale/golibraries/valkey/transcoder"
	valkeygo "github.com/valkey-io/valkey-go"
)

// connectTimeout bounds credential loading and the startup PING.
const connectTimeout = 5 * time.Second

// Client owns one Valkey connection set and hands out the cache, store and publisher views of it.
// It is safe for concurrent use. Create one per process and Close it on shutdown.
type Client struct {
	cfg    Config
	option valkeygo.ClientOption
	raw    valkeygo.Client
	logger gologger.ILogger

	cache     cache.ICache
	store     store.IStore
	publisher pubsub.IPublisher
}

// NewClient validates cfg, connects and PINGs Valkey. On failure it returns an error; a service
// that wants to keep running without a cache can fall back to cache.NewNoOp.
func NewClient(cfg Config, opts ...Option) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	o := clientOptions{transcoder: transcoder.JSON{}}
	for _, opt := range opts {
		opt(&o)
	}
	if o.logger == nil {
		o.logger = common.DefaultLogger()
	}

	option, err := buildValkeyOption(&cfg, &o)
	if err != nil {
		return nil, err
	}

	raw, err := valkeygo.NewClient(option)
	if err != nil {
		return nil, fmt.Errorf("valkey: connecting to %s: %w", cfg.address(), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := raw.Do(ctx, raw.B().Ping().Build()).Error(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("valkey: PING %s: %w", cfg.address(), err)
	}

	return &Client{
		cfg:    cfg,
		option: option,
		raw:    raw,
		logger: o.logger,
		cache: cache.New(raw,
			cache.WithLogger(o.logger),
			cache.WithTranscoder(o.transcoder),
			cache.WithRequestTimeout(cfg.RequestTimeout),
			cache.WithRefreshDuration(cfg.RefreshCacheDuration),
			cache.WithLoadTimeout(cfg.LoadTimeout)),
		store: store.New(raw,
			store.WithTranscoder(o.transcoder),
			store.WithRequestTimeout(cfg.RequestTimeout)),
		publisher: pubsub.NewPublisher(raw,
			pubsub.WithShardedPublish(*cfg.ShardedPubSub),
			pubsub.WithPublishTimeout(cfg.RequestTimeout)),
	}, nil
}

func buildValkeyOption(cfg *Config, o *clientOptions) (valkeygo.ClientOption, error) {
	option := valkeygo.ClientOption{
		InitAddress:       []string{cfg.address()},
		ForceSingleClient: !*cfg.IsCluster,
		ClientName:        cfg.ClientName,
		// Client-side caching needs CLIENT TRACKING, which ElastiCache restricts; the cache API here
		// is server-side only.
		DisableCache: true,
	}
	if *cfg.TLS {
		option.TLSConfig = &tls.Config{
			ServerName:         cfg.Host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // opt-in, local testing only
		}
	}

	switch cfg.AuthMode {
	case AuthIAM:
		credentials := o.credentials
		if credentials == nil {
			ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
			awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
			cancel()
			if err != nil {
				return option, fmt.Errorf("valkey: loading AWS config for IAM auth: %w", err)
			}
			if awsCfg.Credentials == nil {
				return option, errors.New("valkey: no AWS credentials found for IAM auth")
			}
			credentials = awsCfg.Credentials
		}
		provider := iam.NewTokenProvider(cfg.Username, cfg.ClusterName, cfg.Region, cfg.Serverless, credentials)
		option.AuthCredentialsFn = func(valkeygo.AuthCredentialsContext) (valkeygo.AuthCredentials, error) {
			user, token, err := provider.Credentials()
			if err != nil {
				o.logger.LogError("valkey: failed to generate IAM auth token", err)
				return valkeygo.AuthCredentials{}, err
			}
			return valkeygo.AuthCredentials{Username: user, Password: token}, nil
		}
	case AuthPassword:
		option.Username = cfg.Username
		option.Password = cfg.Password
	}

	if o.valkeyModifier != nil {
		o.valkeyModifier(&option)
	}
	return option, nil
}

// Cache returns the cache-aside view: errors are logged and treated as misses.
func (c *Client) Cache() cache.ICache { return c.cache }

// Store returns the system-of-record view: every error is returned to the caller.
func (c *Client) Store() store.IStore { return c.store }

// Publisher returns the pub/sub publisher. It shares the command connections.
func (c *Client) Publisher() pubsub.IPublisher { return c.publisher }

// NewSubscriber opens a separate connection set for subscriptions, so a subscribed connection never
// carries command traffic. Close the subscriber when done; Client.Close does not close it.
func (c *Client) NewSubscriber() (pubsub.ISubscriber, error) {
	option := c.option
	// The subscriber runs its own reconnect loop so it can report drops and resubscriptions.
	option.DisableRetry = true
	raw, err := valkeygo.NewClient(option)
	if err != nil {
		return nil, fmt.Errorf("valkey: connecting subscriber to %s: %w", c.cfg.address(), err)
	}
	return pubsub.NewSubscriber(raw,
		pubsub.WithShardedSubscribe(*c.cfg.ShardedPubSub),
		pubsub.WithSubscriberLogger(c.logger)), nil
}

// Raw exposes the underlying valkey-go client for commands this package does not wrap.
func (c *Client) Raw() valkeygo.Client { return c.raw }

// Config returns the validated configuration, with defaults filled in.
func (c *Client) Config() Config { return c.cfg }

// Close closes the command connections. Subscribers are closed separately.
func (c *Client) Close() { c.raw.Close() }
