package valkey

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// AuthMode selects how the client authenticates to Valkey.
type AuthMode string

const (
	// AuthIAM authenticates with an ElastiCache IAM token. This is the default.
	AuthIAM AuthMode = "iam"
	// AuthPassword authenticates with a static username and password (local development).
	AuthPassword AuthMode = "password"
	// AuthNone sends no AUTH (local development and tests).
	AuthNone AuthMode = "none"
)

const (
	defaultPort                 = 6379
	defaultRequestTimeout       = 250 * time.Millisecond
	defaultRefreshCacheDuration = 10 * time.Second
)

// Config holds the connection settings. It mirrors ValkeyCacheConfig in AEPLCore.Valkey.
type Config struct {
	// Host is the primary or configuration endpoint. Required.
	Host string
	// Port defaults to 6379.
	Port int
	// IsCluster is true for ElastiCache cluster mode and false for standalone. Required, with no
	// default: a wrong guess means a standalone client aimed at a production cluster.
	IsCluster *bool
	// TLS enables TLS. Defaults to true; IAM auth requires it.
	TLS *bool
	// InsecureSkipVerify disables TLS certificate verification. Only for local testing.
	InsecureSkipVerify bool

	// AuthMode defaults to AuthIAM.
	AuthMode AuthMode
	// Username is the IAM user (AuthIAM) or ACL user (AuthPassword).
	Username string
	// Password is used only with AuthPassword.
	Password string
	// ClusterName is the ElastiCache replication group or serverless cache name. Lowercased by
	// Validate, because ElastiCache lowercases it at creation and a case mismatch gives an opaque
	// auth error. Required for AuthIAM.
	ClusterName string
	// Region is the AWS region. Required for AuthIAM.
	Region string
	// Serverless marks an ElastiCache Serverless cache; the IAM token then carries
	// ResourceType=ServerlessCache.
	Serverless bool

	// ClientName is sent with CLIENT SETNAME on every connection.
	ClientName string
	// RequestTimeout bounds each command when the caller's context has no deadline. Default 250ms.
	RequestTimeout time.Duration
	// RefreshCacheDuration is the TTL used by soft expire and the critical-read sentinel. Default 10s.
	RefreshCacheDuration time.Duration
	// ShardedPubSub makes Publish use SPUBLISH and Subscribe use SSUBSCRIBE (Valkey 7+).
	// Defaults to the value of IsCluster: classic PUBLISH in a cluster fans out to every node.
	ShardedPubSub *bool
}

// Bool returns a pointer to b, for the *bool fields of Config.
func Bool(b bool) *bool { return &b }

// Validate fills defaults, normalises fields and reports the first invalid setting.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("valkey: config is nil")
	}
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("valkey: Host is required")
	}
	if c.IsCluster == nil {
		return errors.New("valkey: IsCluster is required (true for cluster mode, false for standalone)")
	}
	if c.Port == 0 {
		c.Port = defaultPort
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("valkey: invalid Port %d", c.Port)
	}
	if c.TLS == nil {
		c.TLS = Bool(true)
	}
	if c.AuthMode == "" {
		c.AuthMode = AuthIAM
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultRequestTimeout
	}
	if c.RefreshCacheDuration <= 0 {
		c.RefreshCacheDuration = defaultRefreshCacheDuration
	}
	if c.ShardedPubSub == nil {
		c.ShardedPubSub = Bool(*c.IsCluster)
	}
	c.ClusterName = strings.ToLower(strings.TrimSpace(c.ClusterName))

	switch c.AuthMode {
	case AuthIAM:
		if !*c.TLS {
			return errors.New("valkey: IAM auth requires TLS; set TLS = true")
		}
		if strings.TrimSpace(c.Username) == "" {
			return errors.New("valkey: Username is required for IAM auth")
		}
		if c.ClusterName == "" {
			return errors.New("valkey: ClusterName is required for IAM auth")
		}
		if strings.TrimSpace(c.Region) == "" {
			return errors.New("valkey: Region is required for IAM auth")
		}
	case AuthPassword:
		if c.Password == "" {
			return errors.New("valkey: Password is required for password auth")
		}
	case AuthNone:
	default:
		return fmt.Errorf("valkey: unknown AuthMode %q", c.AuthMode)
	}
	return nil
}

func (c *Config) address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
