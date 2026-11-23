package cache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carwale/golibraries/valkey/internal/testutil"
)

// A soft expire must never keep a value longer than it would have lived anyway.
func TestSoftExpireNeverExtendsTTL(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	cache.Set(ctx, "short", []byte("v"), 2*time.Second)
	if !cache.Expire(ctx, "short") {
		t.Fatal("Expire failed")
	}
	if ttl := mr.TTL("short"); ttl != 2*time.Second {
		t.Fatalf("TTL after soft expire = %v, want the shorter 2s kept", ttl)
	}
	cache.Set(ctx, "short2", []byte("v"), 2*time.Second)
	if !cache.ExpireMultiple(ctx, []string{"short2"}) || mr.TTL("short2") != 2*time.Second {
		t.Fatalf("ExpireMultiple TTL = %v, want 2s kept", mr.TTL("short2"))
	}
	cache.Set(ctx, "forever", []byte("v"), 0)
	if !cache.Expire(ctx, "forever") || mr.TTL("forever") != 10*time.Second {
		t.Fatalf("key without TTL: TTL = %v, want 10s", mr.TTL("forever"))
	}
}

// A write that lands while a miss is loading must win over the (stale) loaded value.
func TestGetOrLoadMissDoesNotOverwriteCriticalSentinel(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	v, err := GetOrLoad(ctx, cache, "k", time.Hour, func(context.Context) (string, error) {
		// A write happens while the replica read is in flight.
		cache.ExpireWithCriticalRead(ctx, "k")
		return "stale", nil
	})
	if err != nil || v != "stale" {
		t.Fatalf("GetOrLoad = %q, %v", v, err)
	}
	if got, _ := mr.Get("k"); got != CriticalSentinel {
		t.Fatalf("stored %q, want the critical sentinel to survive", got)
	}
	// The next read takes the critical path and replaces the sentinel.
	v, _ = GetOrLoadCritical(ctx, cache, "k", time.Hour,
		func(context.Context) (string, error) { return "replica", nil },
		func(context.Context) (string, error) { return "primary", nil })
	if got, _ := GetAs[string](ctx, cache, "k"); v != "primary" || got != "primary" {
		t.Fatalf("critical load = %q, stored %q", v, got)
	}
}

// One caller giving up must not fail the others sharing its load.
func TestGetOrLoadCallerCancelDoesNotFailOthers(t *testing.T) {
	cache, mr := newTestCache(t)

	var calls int32
	started := make(chan struct{})
	release := make(chan struct{})
	load := func(ctx context.Context) (int, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return 42, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}

	impatient, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := GetOrLoad(impatient, cache, "hot", time.Minute, load)
		firstErr <- err
	}()
	<-started

	second := make(chan int, 1)
	go func() {
		v, err := GetOrLoad(context.Background(), cache, "hot", time.Minute, load)
		if err != nil {
			t.Errorf("second caller: %v", err)
		}
		second <- v
	}()
	time.Sleep(50 * time.Millisecond) // let the second caller join the load

	cancel()
	select {
	case err := <-firstErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first caller err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first caller should stop waiting when its ctx is cancelled")
	}

	close(release)
	select {
	case v := <-second:
		if v != 42 {
			t.Fatalf("second caller = %d, want 42", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second caller never returned")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("loader called %d times, want 1", got)
	}
	if !mr.Exists("hot") {
		t.Fatal("the shared load should still be stored after the first caller left")
	}
}

type sameNameLoader func(ctx context.Context, c ICache, release <-chan struct{}) (string, error)

// Two function-local types named User print the same ("cache.User"); they must not share a load.
func userLoaderA() (reflect.Type, sameNameLoader) {
	type User struct{ A int }
	return reflect.TypeOf(User{}), func(ctx context.Context, c ICache, release <-chan struct{}) (string, error) {
		v, err := GetOrLoad(ctx, c, "user", 0, func(context.Context) (User, error) { <-release; return User{A: 1}, nil })
		return fmt.Sprint(v.A), err
	}
}

func userLoaderB() (reflect.Type, sameNameLoader) {
	type User struct{ B int }
	return reflect.TypeOf(User{}), func(ctx context.Context, c ICache, release <-chan struct{}) (string, error) {
		v, err := GetOrLoad(ctx, c, "user", 0, func(context.Context) (User, error) { <-release; return User{B: 2}, nil })
		return fmt.Sprint(v.B), err
	}
}

func TestGetOrLoadTypesWithTheSameNameNeverShare(t *testing.T) {
	cache := NewNoOp(testutil.DiscardLogger(), nil) // every call misses, so both always load
	typeA, loadA := userLoaderA()
	typeB, loadB := userLoaderB()
	if typeA.String() != typeB.String() {
		t.Fatalf("test setup: types print %q and %q; they must print the same", typeA, typeB)
	}

	release := make(chan struct{})
	var wg sync.WaitGroup
	var gotA, gotB string
	var errA, errB error
	wg.Add(2)
	go func() { defer wg.Done(); gotA, errA = loadA(context.Background(), cache, release) }()
	go func() { defer wg.Done(); gotB, errB = loadB(context.Background(), cache, release) }()
	time.Sleep(50 * time.Millisecond) // both loads in flight together
	close(release)
	wg.Wait()

	if errA != nil || errB != nil || gotA != "1" || gotB != "2" {
		t.Fatalf("A = %q, %v; B = %q, %v; want each its own value", gotA, errA, gotB, errB)
	}
}

func TestGetOrLoadPanicBecomesError(t *testing.T) {
	cache, _ := newTestCache(t)
	_, err := GetOrLoad(context.Background(), cache, "p", time.Minute, func(context.Context) (int, error) {
		panic("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("err = %v, want a panic error", err)
	}
}

// While Valkey is down every read goes to the database, so deduplication matters most then.
func TestGetOrLoadDedupesWhileValkeyIsDown(t *testing.T) {
	for name, cache := range map[string]ICache{
		"noop": NewNoOp(testutil.DiscardLogger(), nil),
		"failing valkey": func() ICache {
			c, mr := newTestCache(t)
			mr.SetError("ERR simulated failure") // not retried by valkey-go, so it fails fast
			return c
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			var calls int32
			release := make(chan struct{})
			load := func(context.Context) (int, error) {
				atomic.AddInt32(&calls, 1)
				<-release
				return 7, nil
			}
			const n = 20
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if v, err := GetOrLoad(context.Background(), cache, "k", time.Minute, load); v != 7 || err != nil {
						t.Errorf("GetOrLoad = %d, %v", v, err)
					}
				}()
			}
			time.Sleep(100 * time.Millisecond)
			close(release)
			wg.Wait()
			if got := atomic.LoadInt32(&calls); got != 1 {
				t.Fatalf("loader called %d times, want 1", got)
			}
		})
	}
}

func TestMultiGetOrLoadLimitsConcurrency(t *testing.T) {
	cache := NewNoOp(testutil.DiscardLogger(), nil)
	var inFlight, peak int32
	load := func(context.Context) (int, error) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return 1, nil
	}
	callbacks := map[string]Callback[int]{}
	for i := 0; i < 30; i++ {
		callbacks[fmt.Sprintf("k%d", i)] = Callback[int]{Load: load}
	}

	got, err := MultiGetOrLoad(context.Background(), cache, callbacks, WithMaxConcurrentLoads(3))
	if err != nil || len(got) != 30 {
		t.Fatalf("MultiGetOrLoad = %d values, %v", len(got), err)
	}
	if p := atomic.LoadInt32(&peak); p > 3 {
		t.Fatalf("%d loaders ran at once, want at most 3", p)
	}
}
