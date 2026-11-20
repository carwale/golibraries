package cache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

// Loader fetches a value from the source of truth (usually a database) on a cache miss.
type Loader[T any] func(ctx context.Context) (T, error)

// TypedResult is Result with the value decoded into T.
type TypedResult[T any] struct {
	IsKeyExist     bool
	IsCriticalRead bool
	Value          T
	// Err is a Valkey error or a decode error.
	Err error
}

// Callback configures one key of MultiGetOrLoad.
type Callback[T any] struct {
	// Load is called on a miss (typically a replica read). Required.
	Load Loader[T]
	// CriticalLoad is called when the key holds the critical sentinel (typically a primary read).
	// Defaults to Load.
	CriticalLoad Loader[T]
	// TTL is applied when the loaded value is stored. 0 stores without expiry.
	TTL time.Duration
}

// GetAs reads key and decodes it into T. ok is false on a miss, an error, the critical sentinel or
// a value that does not decode (logged).
func GetAs[T any](ctx context.Context, c ICache, key string) (value T, ok bool) {
	raw, found := c.Get(ctx, key)
	if !found {
		return value, false
	}
	if err := c.Transcoder().Unmarshal(raw, &value); err != nil {
		logTypedErr(c, "decode", key, err)
		var zero T
		return zero, false
	}
	return value, true
}

// SetAs encodes value and stores it. It returns false when encoding (logged) or the write fails.
func SetAs[T any](ctx context.Context, c ICache, key string, value T, ttl time.Duration) bool {
	raw, err := c.Transcoder().Marshal(value)
	if err != nil {
		logTypedErr(c, "encode", key, err)
		return false
	}
	return c.Set(ctx, key, raw, ttl)
}

// GetWithStatusAs is GetWithStatus with the value decoded into T.
func GetWithStatusAs[T any](ctx context.Context, c ICache, key string) TypedResult[T] {
	r := c.GetWithStatus(ctx, key)
	out := TypedResult[T]{IsKeyExist: r.IsKeyExist, IsCriticalRead: r.IsCriticalRead, Err: r.Err}
	if r.IsKeyExist && !r.IsCriticalRead && r.Err == nil {
		if err := c.Transcoder().Unmarshal(r.Value, &out.Value); err != nil {
			out.Err = fmt.Errorf("valkey: decoding key %q: %w", key, err)
		}
	}
	return out
}

// GetOrLoad is cache-aside: a hit is decoded and returned; a miss calls load, stores the result
// with ttl and returns it. The sentinel written by ExpireWithCriticalRead also calls load.
//
// After a miss the result is stored with SET NX, so a load that started before a concurrent write
// never overwrites the newer value or the critical sentinel. When Valkey is unreachable, load is
// called and its result returned without being stored. A load error is returned as is and nothing
// is cached.
//
// Concurrent calls for the same key and type in this process share one load, also while Valkey is
// down. The shared load runs detached from the callers' cancellation, bounded by the cache's load
// timeout (WithLoadTimeout) or the first caller's deadline, whichever is later; each caller stops
// waiting when its own ctx ends. Callers that share a load receive the same value: if T is a map,
// slice or pointer, treat it as read-only.
func GetOrLoad[T any](ctx context.Context, c ICache, key string, ttl time.Duration, load Loader[T]) (T, error) {
	return getOrLoad(ctx, c, key, ttl, load, load)
}

// GetOrLoadCritical is GetOrLoad for master-replica databases: a miss calls load (replica), while
// the critical sentinel calls criticalLoad (primary), avoiding stale reads during replication lag.
func GetOrLoadCritical[T any](ctx context.Context, c ICache, key string, ttl time.Duration, load, criticalLoad Loader[T]) (T, error) {
	if criticalLoad == nil {
		criticalLoad = load
	}
	return getOrLoad(ctx, c, key, ttl, load, criticalLoad)
}

func getOrLoad[T any](ctx context.Context, c ICache, key string, ttl time.Duration, load, criticalLoad Loader[T]) (T, error) {
	if load == nil {
		var zero T
		return zero, errors.New("valkey: GetOrLoad needs a loader")
	}
	r := c.GetWithStatus(ctx, key)
	switch {
	case r.Err != nil:
		// Valkey is failing: serve from the source and do not write back.
		return loadShared(ctx, c, key, ttl, load, storeNone)
	case r.IsCriticalRead:
		return loadShared(ctx, c, key, ttl, criticalLoad, storeAlways)
	case r.IsKeyExist:
		var v T
		err := c.Transcoder().Unmarshal(r.Value, &v)
		if err == nil {
			return v, nil
		}
		// An undecodable value is replaced with a fresh one.
		logTypedErr(c, "decode", key, err)
		return loadShared(ctx, c, key, ttl, load, storeAlways)
	}
	return loadShared(ctx, c, key, ttl, load, storeIfAbsent)
}

// storeMode says how a loaded value is written back.
type storeMode int

const (
	// storeNone does not write: Valkey failed, or the key was blank.
	storeNone storeMode = iota
	// storeIfAbsent writes with SET NX. Used after a miss, so a stale load cannot overwrite a newer
	// value or the critical sentinel written while it ran.
	storeIfAbsent
	// storeAlways overwrites. Used after a critical read (the sentinel must be replaced) and for an
	// undecodable value.
	storeAlways
)

func (m storeMode) String() string {
	switch m {
	case storeIfAbsent:
		return "miss"
	case storeAlways:
		return "replace"
	default:
		return "nostore"
	}
}

// loadDeduper is implemented by the caches in this package (valkeyCache and NoOp) so concurrent
// loads of the same key share one call. Other ICache implementations (mocks) load directly.
type loadDeduper interface {
	loadGroup(t reflect.Type) *singleflight.Group
	loadTimeout() time.Duration
}

// loadGroups keeps one singleflight group per value type, so two types never share a load even when
// they print the same (a/models.User and b/models.User).
type loadGroups struct {
	groups  sync.Map // reflect.Type -> *singleflight.Group
	timeout time.Duration
}

func (g *loadGroups) loadGroup(t reflect.Type) *singleflight.Group {
	if v, ok := g.groups.Load(t); ok {
		return v.(*singleflight.Group)
	}
	v, _ := g.groups.LoadOrStore(t, new(singleflight.Group))
	return v.(*singleflight.Group)
}

func (g *loadGroups) loadTimeout() time.Duration {
	if g.timeout > 0 {
		return g.timeout
	}
	return DefaultLoadTimeout
}

// loadShared runs load (and the write-back) once per key, type and store mode across concurrent
// callers in this process. Each caller waits only as long as its own ctx allows.
func loadShared[T any](ctx context.Context, c ICache, key string, ttl time.Duration, load Loader[T], mode storeMode) (T, error) {
	// A caller that has already given up must not start a detached load: nobody would read it, and
	// in MultiGetOrLoad every remaining job would start one at once, past the concurrency cap.
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	d, ok := c.(loadDeduper)
	if !ok {
		return loadAndStore(ctx, c, key, ttl, load, mode)
	}
	t := reflect.TypeOf((*T)(nil)).Elem()
	timeout := d.loadTimeout()
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > timeout {
			timeout = remaining
		}
	}
	ch := d.loadGroup(t).DoChan(mode.String()+"\x00"+key, func() (v any, err error) {
		// singleflight re-panics a DoChan panic on a new goroutine, which would crash the process.
		defer func() {
			if p := recover(); p != nil {
				v, err = nil, fmt.Errorf("valkey: loader for key %q panicked: %v", key, p)
			}
		}()
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		return loadAndStore(lctx, c, key, ttl, load, mode)
	})

	var zero T
	select {
	case res := <-ch:
		if res.Val == nil {
			return zero, res.Err
		}
		v, ok := res.Val.(T)
		if !ok {
			return zero, fmt.Errorf("valkey: shared load for key %q returned %T, want %v", key, res.Val, t)
		}
		return v, res.Err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func loadAndStore[T any](ctx context.Context, c ICache, key string, ttl time.Duration, load Loader[T], mode storeMode) (T, error) {
	v, err := load(ctx)
	if err != nil {
		return v, err
	}
	switch mode {
	case storeIfAbsent:
		if raw, err := c.Transcoder().Marshal(v); err == nil {
			c.SetNX(ctx, key, raw, ttl)
		} else {
			logTypedErr(c, "encode", key, err)
		}
	case storeAlways:
		SetAs(ctx, c, key, v, ttl)
	}
	return v, nil
}

// MultiGetAs reads many keys and decodes the hits. Misses, sentinels, errors and undecodable values
// (logged) are omitted.
func MultiGetAs[T any](ctx context.Context, c ICache, keys []string) map[string]T {
	raw := c.MultiGet(ctx, keys)
	out := make(map[string]T, len(raw))
	for k, b := range raw {
		var v T
		if err := c.Transcoder().Unmarshal(b, &v); err != nil {
			logTypedErr(c, "decode", k, err)
			continue
		}
		out[k] = v
	}
	return out
}

// DefaultMaxConcurrentLoads is how many loaders MultiGetOrLoad runs at once by default.
const DefaultMaxConcurrentLoads = 8

// MultiOption customises MultiGetOrLoad.
type MultiOption func(*multiOptions)

type multiOptions struct {
	maxConcurrent int
}

// WithMaxConcurrentLoads caps how many loaders MultiGetOrLoad runs at once (default 8), so a cache
// outage does not turn one request into hundreds of parallel database queries.
func WithMaxConcurrentLoads(n int) MultiOption {
	return func(o *multiOptions) {
		if n > 0 {
			o.maxConcurrent = n
		}
	}
}

// MultiGetOrLoad reads every key of callbacks in one MultiGet and, for each miss or critical
// sentinel, calls that key's loader and stores the result like GetOrLoad does (sharing loads with
// concurrent callers too). At most WithMaxConcurrentLoads loaders run at once. It returns every value
// it could get and the joined errors of the loaders that failed.
func MultiGetOrLoad[T any](ctx context.Context, c ICache, callbacks map[string]Callback[T], opts ...MultiOption) (map[string]T, error) {
	o := multiOptions{maxConcurrent: DefaultMaxConcurrentLoads}
	for _, opt := range opts {
		opt(&o)
	}
	keys := make([]string, 0, len(callbacks))
	for k := range callbacks {
		keys = append(keys, k)
	}
	statuses := c.MultiGetWithStatus(ctx, keys)

	type job struct {
		key  string
		load Loader[T]
		ttl  time.Duration
		mode storeMode
	}
	out := make(map[string]T, len(callbacks))
	var (
		jobs []job
		errs []error
	)
	// First pass, single goroutine: collect hits and decide what to load.
	for key, cb := range callbacks {
		r, found := statuses[key]
		mode := storeIfAbsent
		switch {
		case !found || r.Err != nil:
			// Keys missing from statuses were blank; like a Valkey error, do not store them.
			mode = storeNone
		case r.IsCriticalRead:
			mode = storeAlways
		case r.IsKeyExist:
			var v T
			err := c.Transcoder().Unmarshal(r.Value, &v)
			if err == nil {
				out[key] = v
				continue
			}
			logTypedErr(c, "decode", key, err)
			mode = storeAlways // undecodable: replace it
		}
		load := cb.Load
		if r.IsCriticalRead && cb.CriticalLoad != nil {
			load = cb.CriticalLoad
		}
		if load == nil {
			errs = append(errs, fmt.Errorf("valkey: no loader for key %q", key))
			continue
		}
		jobs = append(jobs, job{key: key, load: load, ttl: cb.TTL, mode: mode})
	}

	// Second pass: run the loaders, at most maxConcurrent at a time.
	var (
		mu sync.Mutex
		g  errgroup.Group
	)
	g.SetLimit(o.maxConcurrent)
	for _, j := range jobs {
		g.Go(func() error {
			v, err := loadShared(ctx, c, j.key, j.ttl, j.load, j.mode)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("valkey: loading key %q: %w", j.key, err))
				return nil
			}
			out[j.key] = v
			return nil
		})
	}
	_ = g.Wait()
	return out, errors.Join(errs...)
}
