package valkey

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/valkey/transcoder"
	valkeygo "github.com/valkey-io/valkey-go"
)

// Option customises a Client.
type Option func(c *clientOptions)

type clientOptions struct {
	logger         gologger.ILogger
	transcoder     transcoder.ITranscoder
	credentials    aws.CredentialsProvider
	valkeyModifier func(*valkeygo.ClientOption)
}

// WithLogger sets the logger. Defaults to the gologger zerolog logger.
func WithLogger(logger gologger.ILogger) Option {
	return func(c *clientOptions) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// WithTranscoder replaces the default JSON transcoder used by the typed cache and store helpers.
func WithTranscoder(t transcoder.ITranscoder) Option {
	return func(c *clientOptions) {
		if t != nil {
			c.transcoder = t
		}
	}
}

// WithAWSCredentials sets the credentials used to sign IAM auth tokens. Defaults to the AWS default
// credential chain (environment, IRSA/web identity, ECS/EC2 role).
func WithAWSCredentials(provider aws.CredentialsProvider) Option {
	return func(c *clientOptions) {
		if provider != nil {
			c.credentials = provider
		}
	}
}

// WithAWSConfig uses the credentials of an already loaded aws.Config for IAM auth.
func WithAWSConfig(cfg aws.Config) Option {
	return WithAWSCredentials(cfg.Credentials)
}

// WithValkeyOption gives direct access to the underlying valkey-go ClientOption after the library
// has filled it (for example ConnLifetime or BlockingPoolSize). Use with care.
func WithValkeyOption(fn func(*valkeygo.ClientOption)) Option {
	return func(c *clientOptions) { c.valkeyModifier = fn }
}
