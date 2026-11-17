package cache

import (
	"time"

	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/valkey/transcoder"
)

// Option customises the cache returned by New.
type Option func(c *valkeyCache)

// WithLogger sets the logger. Defaults to the gologger zerolog logger.
func WithLogger(logger gologger.ILogger) Option {
	return func(c *valkeyCache) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// WithTranscoder replaces the default JSON transcoder used by the typed helpers.
func WithTranscoder(t transcoder.ITranscoder) Option {
	return func(c *valkeyCache) {
		if t != nil {
			c.transcoder = t
		}
	}
}

// WithRequestTimeout bounds each command when the caller's context has no deadline. Default 250ms.
func WithRequestTimeout(d time.Duration) Option {
	return func(c *valkeyCache) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithRefreshDuration sets the TTL for soft expire and the critical-read sentinel. Default 10s.
func WithRefreshDuration(d time.Duration) Option {
	return func(c *valkeyCache) {
		if d > 0 {
			c.refresh = d
		}
	}
}
