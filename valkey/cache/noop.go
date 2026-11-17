package cache

import (
	"context"
	"time"

	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/valkey/internal/common"
	"github.com/carwale/golibraries/valkey/transcoder"
)

const noOpMessage = "Could not connect to Valkey."

// NoOp is an ICache that never touches Valkey. Use it when connecting fails and the service should
// keep running without a cache. Every call logs "Could not connect to Valkey." so the outage is
// visible, and reports a miss carrying ErrNoConnection, so GetOrLoad still calls the loader (and
// does not try to store the result).
type NoOp struct {
	logger     gologger.ILogger
	transcoder transcoder.ITranscoder
}

// NewNoOp returns a NoOp cache. A nil logger uses the gologger zerolog logger; a nil transcoder
// uses JSON.
func NewNoOp(logger gologger.ILogger, t transcoder.ITranscoder) *NoOp {
	if logger == nil {
		logger = common.DefaultLogger()
	}
	if t == nil {
		t = transcoder.JSON{}
	}
	return &NoOp{logger: logger, transcoder: t}
}

var _ ICache = (*NoOp)(nil)

func (n *NoOp) log() { n.logger.LogErrorWithoutError(noOpMessage) }

// Transcoder returns the configured transcoder.
func (n *NoOp) Transcoder() transcoder.ITranscoder { return n.transcoder }

// Get always misses.
func (n *NoOp) Get(context.Context, string) ([]byte, bool) { n.log(); return nil, false }

// GetWithStatus always reports a miss with ErrNoConnection.
func (n *NoOp) GetWithStatus(context.Context, string) Result {
	n.log()
	return Result{Err: ErrNoConnection}
}

// Set does nothing and returns false.
func (n *NoOp) Set(context.Context, string, []byte, time.Duration) bool { n.log(); return false }

// Delete does nothing and returns false.
func (n *NoOp) Delete(context.Context, string) bool { n.log(); return false }

// Exists returns false.
func (n *NoOp) Exists(context.Context, string) bool { n.log(); return false }

// MultiGet returns an empty map.
func (n *NoOp) MultiGet(context.Context, []string) map[string][]byte {
	n.log()
	return map[string][]byte{}
}

// MultiGetWithStatus reports every key as a miss with ErrNoConnection.
func (n *NoOp) MultiGetWithStatus(_ context.Context, keys []string) map[string]Result {
	n.log()
	out := make(map[string]Result, len(keys))
	for _, k := range common.UniqueKeys(keys) {
		out[k] = Result{Err: ErrNoConnection}
	}
	return out
}

// Expire returns false.
func (n *NoOp) Expire(context.Context, string) bool { n.log(); return false }

// ExpireWithDelay returns false.
func (n *NoOp) ExpireWithDelay(context.Context, string, time.Duration) bool {
	n.log()
	return false
}

// ExpireWithoutDelay returns false.
func (n *NoOp) ExpireWithoutDelay(context.Context, string) bool { n.log(); return false }

// ExpireMultiple returns false.
func (n *NoOp) ExpireMultiple(context.Context, []string) bool { n.log(); return false }

// ExpireMultipleWithoutDelay returns false.
func (n *NoOp) ExpireMultipleWithoutDelay(context.Context, []string) bool {
	n.log()
	return false
}

// ExpireWithCriticalRead returns false.
func (n *NoOp) ExpireWithCriticalRead(context.Context, string) bool { n.log(); return false }
