package cache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

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

// loadGrouper lets the real cache deduplicate concurrent loads of the same key in this process.
type loadGrouper interface {
	loadGroup() *singleflight.Group
}

// GetAs reads key and decodes it into T. ok is false on a miss, an error, the critical sentinel or
// a value that does not decode.
func GetAs[T any](ctx context.Context, c ICache, key string) (value T, ok bool) {
	raw, found := c.Get(ctx, key)
	if !found {
		return value, false
	}
	if err := c.Transcoder().Unmarshal(raw, &value); err != nil {
		var zero T
		return zero, false
	}
	return value, true
}

// SetAs encodes value and stores it. It returns false when encoding or the write fails.
func SetAs[T any](ctx context.Context, c ICache, key string, value T, ttl time.Duration) bool {
	raw, err := c.Transcoder().Marshal(value)
	if err != nil {
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
// When Valkey is unreachable, load is called and its result returned without being stored. A load
// error is returned as is and nothing is cached. Concurrent misses for the same key in this process
// share one load call (the first caller's ctx is used for it).
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
		return load(ctx)
	case r.IsCriticalRead:
		return loadAndStore(ctx, c, key, ttl, criticalLoad, "critical")
	case r.IsKeyExist:
		var v T
		if err := c.Transcoder().Unmarshal(r.Value, &v); err == nil {
			return v, nil
		}
		// An undecodable value is replaced with a fresh one.
	}
	return loadAndStore(ctx, c, key, ttl, load, "miss")
}

func loadAndStore[T any](ctx context.Context, c ICache, key string, ttl time.Duration, load Loader[T], kind string) (T, error) {
	run := func() (T, error) {
		v, err := load(ctx)
		if err != nil {
			return v, err
		}
		SetAs(ctx, c, key, v, ttl)
		return v, nil
	}

	g, ok := c.(loadGrouper)
	if !ok {
		return run()
	}
	// The type is part of the key so callers loading the same key as different types never share.
	sfKey := kind + "\x00" + reflect.TypeOf((*T)(nil)).Elem().String() + "\x00" + key
	res, err, _ := g.loadGroup().Do(sfKey, func() (any, error) { return run() })
	v, _ := res.(T)
	return v, err
}

// MultiGetAs reads many keys and decodes the hits. Misses, sentinels, errors and undecodable values
// are omitted.
func MultiGetAs[T any](ctx context.Context, c ICache, keys []string) map[string]T {
	raw := c.MultiGet(ctx, keys)
	out := make(map[string]T, len(raw))
	for k, b := range raw {
		var v T
		if err := c.Transcoder().Unmarshal(b, &v); err == nil {
			out[k] = v
		}
	}
	return out
}

// MultiGetOrLoad reads every key of callbacks in one MultiGet and, for each miss or critical
// sentinel, calls that key's loader concurrently and stores the result. It returns every value it
// could get and the joined errors of the loaders that failed.
func MultiGetOrLoad[T any](ctx context.Context, c ICache, callbacks map[string]Callback[T]) (map[string]T, error) {
	keys := make([]string, 0, len(callbacks))
	for k := range callbacks {
		keys = append(keys, k)
	}
	statuses := c.MultiGetWithStatus(ctx, keys)

	type job struct {
		key   string
		load  Loader[T]
		ttl   time.Duration
		store bool
	}
	out := make(map[string]T, len(callbacks))
	var (
		jobs []job
		errs []error
	)
	// First pass, single goroutine: collect hits and decide what to load.
	for key, cb := range callbacks {
		r, found := statuses[key]
		if found && r.Err == nil && r.IsKeyExist && !r.IsCriticalRead {
			var v T
			if err := c.Transcoder().Unmarshal(r.Value, &v); err == nil {
				out[key] = v
				continue
			}
		}
		load := cb.Load
		if r.IsCriticalRead && cb.CriticalLoad != nil {
			load = cb.CriticalLoad
		}
		if load == nil {
			errs = append(errs, fmt.Errorf("valkey: no loader for key %q", key))
			continue
		}
		// Keys missing from statuses were blank; like a Valkey error, do not store them.
		jobs = append(jobs, job{key: key, load: load, ttl: cb.TTL, store: found && r.Err == nil})
	}

	// Second pass: run the loaders concurrently.
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			v, err := j.load(ctx)
			if err == nil && j.store {
				SetAs(ctx, c, j.key, v, j.ttl)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("valkey: loading key %q: %w", j.key, err))
				return
			}
			out[j.key] = v
		}(j)
	}
	wg.Wait()
	return out, errors.Join(errs...)
}
