// Package cache is the cache-aside API on Valkey, a port of IValkeyCache from the C# AEPLCore.Valkey
// library. Every method logs and swallows errors: a failing cache degrades to misses and false,
// never to a failed request.
//
// Methods work on raw bytes; typed access is provided by the generic functions GetAs, SetAs,
// GetOrLoad, GetOrLoadCritical, MultiGetAs and MultiGetOrLoad, which work on any ICache (including
// mocks and NoOp).
package cache

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/valkey/internal/common"
	"github.com/carwale/golibraries/valkey/transcoder"
	valkeygo "github.com/valkey-io/valkey-go"
)

// CriticalSentinel is the value ExpireWithCriticalRead stores in place of a cached value. A reader
// that sees it must go to the primary database. It is byte-identical to the C# AEPLCore.Valkey
// sentinel, so Go and C# services can share keys.
const CriticalSentinel = "_valkey_critical_"

// DefaultRefreshDuration is the default TTL for soft expire and the critical-read sentinel.
const DefaultRefreshDuration = 10 * time.Second

// DefaultLoadTimeout bounds a shared GetOrLoad / MultiGetOrLoad load when the caller's deadline is
// shorter (or absent). See WithLoadTimeout.
const DefaultLoadTimeout = 5 * time.Second

var criticalSentinel = []byte(CriticalSentinel)

var (
	// ErrEmptyKey is reported (in Result.Err) for an empty or blank key.
	ErrEmptyKey = common.ErrEmptyKey
	// ErrNoConnection is reported by the NoOp cache on every call.
	ErrNoConnection = errors.New("valkey: could not connect to Valkey")
)

// Result is a status-aware cache read.
type Result struct {
	// IsKeyExist is false on a miss (and on an error).
	IsKeyExist bool
	// IsCriticalRead is true when the key holds the critical-read sentinel; Value is then empty and
	// the caller should read from the primary database.
	IsCriticalRead bool
	// Value is the raw stored value on a normal hit.
	Value []byte
	// Err is set when Valkey could not be read (already logged). The result then counts as a miss,
	// but a cache-aside caller should not write the loaded value back.
	Err error
}

// ICache is the cache-aside API.
type ICache interface {
	// Get returns the raw value. ok is false on a miss, an error or the critical sentinel.
	Get(ctx context.Context, key string) (value []byte, ok bool)
	// GetWithStatus returns the value together with miss / critical-read / error status.
	GetWithStatus(ctx context.Context, key string) Result
	// Set stores value. A ttl of 0 or less stores it without expiry.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) bool
	// SetNX stores value only if the key does not exist and reports whether it was stored. GetOrLoad
	// uses it after a miss, so a slow load never overwrites a newer value or the critical sentinel.
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) bool
	// Delete removes the key and reports whether it existed.
	Delete(ctx context.Context, key string) bool
	// Exists reports whether the key exists.
	Exists(ctx context.Context, key string) bool
	// MultiGet reads many keys in as few round trips as possible. Misses, sentinels and errors are
	// omitted from the result.
	MultiGet(ctx context.Context, keys []string) map[string][]byte
	// MultiGetWithStatus is MultiGet with a status per requested key.
	MultiGetWithStatus(ctx context.Context, keys []string) map[string]Result

	// Expire soft-expires the key: it stays readable for at most the refresh duration (default 10s)
	// and then expires, instead of being deleted now. Covers the replica-lag window after a write. A
	// key whose remaining TTL is already shorter keeps it (PEXPIRE ... LT, Valkey 7+).
	Expire(ctx context.Context, key string) bool
	// ExpireWithDelay is Expire with a custom TTL. A ttl of 0 or less deletes the key now.
	ExpireWithDelay(ctx context.Context, key string, ttl time.Duration) bool
	// ExpireWithoutDelay deletes the key now.
	ExpireWithoutDelay(ctx context.Context, key string) bool
	// ExpireMultiple soft-expires many keys in one pipeline, like Expire.
	ExpireMultiple(ctx context.Context, keys []string) bool
	// ExpireMultipleWithoutDelay deletes many keys in one pipeline.
	ExpireMultipleWithoutDelay(ctx context.Context, keys []string) bool
	// ExpireWithCriticalRead replaces the value with the critical sentinel for the refresh
	// duration, so the next cache-aside read goes to the primary database.
	ExpireWithCriticalRead(ctx context.Context, key string) bool

	// Transcoder returns the encoder used by the typed helpers.
	Transcoder() transcoder.ITranscoder
}

type valkeyCache struct {
	client     valkeygo.Client
	timeout    time.Duration
	refresh    time.Duration
	logger     gologger.ILogger
	transcoder transcoder.ITranscoder
	loadGroups
}

var _ ICache = (*valkeyCache)(nil)

// New returns an ICache on an existing valkey-go client. Most services get it from
// valkey.Client.Cache instead.
func New(client valkeygo.Client, opts ...Option) ICache {
	c := &valkeyCache{
		client:     client,
		timeout:    common.DefaultRequestTimeout,
		refresh:    DefaultRefreshDuration,
		transcoder: transcoder.JSON{},
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.logger == nil {
		c.logger = common.DefaultLogger()
	}
	return c
}

func (c *valkeyCache) Transcoder() transcoder.ITranscoder { return c.transcoder }

func (c *valkeyCache) logErr(op, key string, err error) {
	c.logger.LogError("valkey: "+op+" failed for key: "+key, err)
}

// errLogger is implemented by the caches in this package (valkeyCache and NoOp), so the typed
// helpers can log encode and decode errors. Other ICache implementations (mocks) are not logged to.
type errLogger interface {
	logErr(op, key string, err error)
}

// logTypedErr logs an encode or decode error of a typed helper through c's logger.
func logTypedErr(c ICache, op, key string, err error) {
	if l, ok := c.(errLogger); ok {
		l.logErr(op, key, err)
	}
}

func (c *valkeyCache) Get(ctx context.Context, key string) ([]byte, bool) {
	r := c.GetWithStatus(ctx, key)
	if !r.IsKeyExist || r.IsCriticalRead {
		return nil, false
	}
	return r.Value, true
}

func (c *valkeyCache) GetWithStatus(ctx context.Context, key string) Result {
	if common.Blank(key) {
		c.logErr("Get", key, ErrEmptyKey)
		return Result{Err: ErrEmptyKey}
	}
	ctx, cancel := common.CapTimeout(ctx, c.timeout)
	defer cancel()

	v, err := c.client.Do(ctx, c.client.B().Get().Key(key).Build()).AsBytes()
	if err != nil {
		if valkeygo.IsValkeyNil(err) {
			return Result{}
		}
		c.logErr("Get", key, err)
		return Result{Err: err}
	}
	return toResult(v)
}

func toResult(v []byte) Result {
	if bytes.Equal(v, criticalSentinel) {
		return Result{IsKeyExist: true, IsCriticalRead: true}
	}
	return Result{IsKeyExist: true, Value: v}
}

func (c *valkeyCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) bool {
	if common.Blank(key) {
		c.logErr("Set", key, ErrEmptyKey)
		return false
	}
	ctx, cancel := common.CapTimeout(ctx, c.timeout)
	defer cancel()

	if err := c.client.Do(ctx, common.SetCmd(c.client, key, value, ttl)).Error(); err != nil {
		c.logErr("Set", key, err)
		return false
	}
	return true
}

func (c *valkeyCache) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) bool {
	if common.Blank(key) {
		c.logErr("SetNX", key, ErrEmptyKey)
		return false
	}
	ctx, cancel := common.CapTimeout(ctx, c.timeout)
	defer cancel()

	err := c.client.Do(ctx, common.SetNXCmd(c.client, key, value, ttl)).Error()
	if valkeygo.IsValkeyNil(err) {
		return false // the key already exists
	}
	if err != nil {
		c.logErr("SetNX", key, err)
		return false
	}
	return true
}

func (c *valkeyCache) Delete(ctx context.Context, key string) bool {
	if common.Blank(key) {
		c.logErr("Delete", key, ErrEmptyKey)
		return false
	}
	ctx, cancel := common.CapTimeout(ctx, c.timeout)
	defer cancel()

	n, err := c.client.Do(ctx, c.client.B().Del().Key(key).Build()).AsInt64()
	if err != nil {
		c.logErr("Delete", key, err)
		return false
	}
	return n > 0
}

func (c *valkeyCache) Exists(ctx context.Context, key string) bool {
	if common.Blank(key) {
		c.logErr("Exists", key, ErrEmptyKey)
		return false
	}
	ctx, cancel := common.CapTimeout(ctx, c.timeout)
	defer cancel()

	n, err := c.client.Do(ctx, c.client.B().Exists().Key(key).Build()).AsInt64()
	if err != nil {
		c.logErr("Exists", key, err)
		return false
	}
	return n > 0
}

func (c *valkeyCache) MultiGet(ctx context.Context, keys []string) map[string][]byte {
	statuses := c.MultiGetWithStatus(ctx, keys)
	out := make(map[string][]byte, len(statuses))
	for k, r := range statuses {
		if r.IsKeyExist && !r.IsCriticalRead {
			out[k] = r.Value
		}
	}
	return out
}

func (c *valkeyCache) MultiGetWithStatus(ctx context.Context, keys []string) map[string]Result {
	unique := common.UniqueKeys(keys)
	out := make(map[string]Result, len(unique))
	if len(unique) == 0 {
		return out
	}
	ctx, cancel := common.CapTimeout(ctx, c.timeout)
	defer cancel()

	// valkeygo.MGet groups keys by slot in cluster mode, so cross-slot keys are safe.
	msgs, err := valkeygo.MGet(c.client, ctx, unique)
	if err != nil {
		c.logger.LogError("valkey: MultiGet failed", err)
		for _, k := range unique {
			out[k] = Result{Err: err}
		}
		return out
	}
	for _, k := range unique {
		msg, ok := msgs[k]
		if !ok || msg.IsNil() {
			out[k] = Result{}
			continue
		}
		v, err := msg.AsBytes()
		if err != nil {
			if valkeygo.IsValkeyNil(err) {
				out[k] = Result{}
				continue
			}
			c.logErr("MultiGet", k, err)
			out[k] = Result{Err: err}
			continue
		}
		out[k] = toResult(v)
	}
	return out
}

func (c *valkeyCache) Expire(ctx context.Context, key string) bool {
	return c.ExpireWithDelay(ctx, key, c.refresh)
}

// ExpireWithDelay uses PEXPIRE with LT: the TTL is set to ttl unless the key already expires
// sooner, so invalidating never keeps stale data longer than it would have lived anyway (a key
// without a TTL counts as infinite and gets ttl). The C# library re-stores the value with the new
// TTL, which can extend it.
func (c *valkeyCache) ExpireWithDelay(ctx context.Context, key string, ttl time.Duration) bool {
	if common.Blank(key) {
		c.logErr("ExpireWithDelay", key, ErrEmptyKey)
		return false
	}
	if ttl <= 0 {
		return c.ExpireWithoutDelay(ctx, key)
	}
	ctx, cancel := common.CapTimeout(ctx, c.timeout)
	defer cancel()

	// A missing key counts as success, as in the C# library.
	cmd := c.client.B().Pexpire().Key(key).Milliseconds(common.DurationMillis(ttl)).Lt().Build()
	if err := c.client.Do(ctx, cmd).Error(); err != nil {
		c.logErr("ExpireWithDelay", key, err)
		return false
	}
	return true
}

func (c *valkeyCache) ExpireWithoutDelay(ctx context.Context, key string) bool {
	if common.Blank(key) {
		c.logErr("ExpireWithoutDelay", key, ErrEmptyKey)
		return false
	}
	ctx, cancel := common.CapTimeout(ctx, c.timeout)
	defer cancel()

	if err := c.client.Do(ctx, c.client.B().Del().Key(key).Build()).Error(); err != nil {
		c.logErr("ExpireWithoutDelay", key, err)
		return false
	}
	return true
}

func (c *valkeyCache) ExpireMultiple(ctx context.Context, keys []string) bool {
	ms := common.DurationMillis(c.refresh)
	return c.multi(ctx, "ExpireMultiple", keys, func(k string) valkeygo.Completed {
		return c.client.B().Pexpire().Key(k).Milliseconds(ms).Lt().Build()
	})
}

func (c *valkeyCache) ExpireMultipleWithoutDelay(ctx context.Context, keys []string) bool {
	return c.multi(ctx, "ExpireMultipleWithoutDelay", keys, func(k string) valkeygo.Completed {
		return c.client.B().Del().Key(k).Build()
	})
}

// multi pipelines one command per key; valkey-go routes each to its slot in cluster mode.
func (c *valkeyCache) multi(ctx context.Context, op string, keys []string, build func(string) valkeygo.Completed) bool {
	unique := common.UniqueKeys(keys)
	if len(unique) == 0 {
		return true
	}
	ctx, cancel := common.CapTimeout(ctx, c.timeout)
	defer cancel()

	cmds := make(valkeygo.Commands, 0, len(unique))
	for _, k := range unique {
		cmds = append(cmds, build(k))
	}
	ok := true
	for i, r := range c.client.DoMulti(ctx, cmds...) {
		if err := r.Error(); err != nil {
			c.logErr(op, unique[i], err)
			ok = false
		}
	}
	return ok
}

func (c *valkeyCache) ExpireWithCriticalRead(ctx context.Context, key string) bool {
	return c.Set(ctx, key, criticalSentinel, c.refresh)
}
