// Package common holds helpers shared by the cache, store and pubsub packages.
package common

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/carwale/golibraries/gologger"
	valkeygo "github.com/valkey-io/valkey-go"
)

// DefaultRequestTimeout bounds a command when the caller's context has no deadline.
const DefaultRequestTimeout = 250 * time.Millisecond

// ErrEmptyKey is reported for an empty or blank key. cache.ErrEmptyKey and store.ErrEmptyKey are
// this same value, so errors.Is works across packages.
var ErrEmptyKey = errors.New("valkey: key is empty")

// DefaultLogger returns the gologger zerolog logger, the default across golibraries.
func DefaultLogger() gologger.ILogger {
	return gologger.NewLoggerFactory().CreateZerologLogger()
}

// Blank reports whether key is empty or whitespace.
func Blank(key string) bool { return strings.TrimSpace(key) == "" }

// UniqueKeys drops blank and duplicate keys, keeping order.
func UniqueKeys(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if Blank(k) {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// DurationMillis rounds up so a sub-millisecond TTL never becomes 0 (which Valkey rejects).
func DurationMillis(d time.Duration) int64 {
	ms := int64(d / time.Millisecond)
	if d%time.Millisecond != 0 {
		ms++
	}
	return ms
}

// WithTimeout applies d when ctx carries no deadline of its own.
func WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok || d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// SetCmd builds SET key value [PX ttl]. A ttl of 0 or less stores without expiry.
func SetCmd(client valkeygo.Client, key string, value []byte, ttl time.Duration) valkeygo.Completed {
	cmd := client.B().Set().Key(key).Value(valkeygo.BinaryString(value))
	if ttl > 0 {
		return cmd.PxMilliseconds(DurationMillis(ttl)).Build()
	}
	return cmd.Build()
}
