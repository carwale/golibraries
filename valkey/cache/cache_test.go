package cache

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/carwale/golibraries/valkey/internal/testutil"
)

type dto struct {
	ID   int    `json:"Id"`
	Name string `json:"Name"`
}

func TestCacheRawOperations(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	if _, ok := cache.Get(ctx, "missing"); ok {
		t.Fatal("Get on a missing key should miss")
	}
	if !cache.Set(ctx, "k", []byte("v"), time.Minute) {
		t.Fatal("Set failed")
	}
	if v, ok := cache.Get(ctx, "k"); !ok || string(v) != "v" {
		t.Fatalf("Get = %q, %v", v, ok)
	}
	if ttl := mr.TTL("k"); ttl != time.Minute {
		t.Fatalf("TTL = %v, want 1m", ttl)
	}
	if !cache.Set(ctx, "forever", []byte("v"), 0) || mr.TTL("forever") != 0 {
		t.Fatal("Set with ttl 0 should store without expiry")
	}
	if !cache.Exists(ctx, "k") || cache.Exists(ctx, "missing") {
		t.Fatal("Exists mismatch")
	}
	if !cache.Delete(ctx, "k") || cache.Delete(ctx, "k") {
		t.Fatal("Delete should report whether the key existed")
	}

	// Blank keys are logged and treated as misses, never panics.
	if cache.Set(ctx, " ", []byte("v"), 0) {
		t.Fatal("Set with a blank key should fail")
	}
	if r := cache.GetWithStatus(ctx, ""); !errors.Is(r.Err, ErrEmptyKey) {
		t.Fatalf("GetWithStatus blank key err = %v", r.Err)
	}
}

func TestCacheCriticalReadAndSoftExpire(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	cache.Set(ctx, "k", []byte("v"), time.Hour)
	if !cache.Expire(ctx, "k") {
		t.Fatal("Expire failed")
	}
	if ttl := mr.TTL("k"); ttl != 10*time.Second {
		t.Fatalf("soft expire TTL = %v, want 10s", ttl)
	}
	if v, ok := cache.Get(ctx, "k"); !ok || string(v) != "v" {
		t.Fatal("soft-expired value should still be readable")
	}
	mr.FastForward(11 * time.Second)
	if _, ok := cache.Get(ctx, "k"); ok {
		t.Fatal("value should be gone after the refresh duration")
	}
	if !cache.Expire(ctx, "missing") {
		t.Fatal("soft expire of a missing key counts as success")
	}

	cache.Set(ctx, "k2", []byte("v"), time.Hour)
	if !cache.ExpireWithDelay(ctx, "k2", 30*time.Second) || mr.TTL("k2") != 30*time.Second {
		t.Fatalf("ExpireWithDelay TTL = %v", mr.TTL("k2"))
	}

	if !cache.ExpireWithCriticalRead(ctx, "k3") {
		t.Fatal("ExpireWithCriticalRead failed")
	}
	if got, _ := mr.Get("k3"); got != CriticalSentinel {
		t.Fatalf("stored %q, want the critical sentinel", got)
	}
	if mr.TTL("k3") != 10*time.Second {
		t.Fatalf("sentinel TTL = %v", mr.TTL("k3"))
	}
	r := cache.GetWithStatus(ctx, "k3")
	if !r.IsKeyExist || !r.IsCriticalRead || r.Value != nil {
		t.Fatalf("GetWithStatus = %+v, want critical read", r)
	}
	if _, ok := cache.Get(ctx, "k3"); ok {
		t.Fatal("Get should miss on the sentinel")
	}

	cache.Set(ctx, "a", []byte("1"), 0)
	cache.Set(ctx, "b", []byte("2"), 0)
	if !cache.ExpireMultiple(ctx, []string{"a", "b", "missing"}) {
		t.Fatal("ExpireMultiple failed")
	}
	if mr.TTL("a") != 10*time.Second || mr.TTL("b") != 10*time.Second {
		t.Fatal("ExpireMultiple should soft-expire every key")
	}
	if !cache.ExpireMultipleWithoutDelay(ctx, []string{"a", "b"}) || mr.Exists("a") || mr.Exists("b") {
		t.Fatal("ExpireMultipleWithoutDelay should delete every key")
	}
	cache.Set(ctx, "c", []byte("3"), 0)
	if !cache.ExpireWithoutDelay(ctx, "c") || mr.Exists("c") {
		t.Fatal("ExpireWithoutDelay should delete the key")
	}
}

func TestCacheMultiGet(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()

	cache.Set(ctx, "a", []byte("1"), 0)
	cache.Set(ctx, "b", []byte("2"), 0)
	cache.ExpireWithCriticalRead(ctx, "crit")

	got := cache.MultiGet(ctx, []string{"a", "b", "crit", "missing", "a", ""})
	if len(got) != 2 || string(got["a"]) != "1" || string(got["b"]) != "2" {
		t.Fatalf("MultiGet = %v", got)
	}
	st := cache.MultiGetWithStatus(ctx, []string{"a", "crit", "missing"})
	if !st["a"].IsKeyExist || !st["crit"].IsCriticalRead || st["missing"].IsKeyExist {
		t.Fatalf("MultiGetWithStatus = %+v", st)
	}
	if len(cache.MultiGet(ctx, nil)) != 0 {
		t.Fatal("MultiGet(nil) should be empty")
	}
}

func TestCacheErrorsAreSwallowed(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()
	mr.SetError("LOADING simulated failure")

	if _, ok := cache.Get(ctx, "k"); ok {
		t.Fatal("Get should miss on error")
	}
	if r := cache.GetWithStatus(ctx, "k"); r.Err == nil || r.IsKeyExist {
		t.Fatalf("GetWithStatus = %+v, want error status", r)
	}
	if cache.Set(ctx, "k", []byte("v"), 0) || cache.Exists(ctx, "k") || cache.Delete(ctx, "k") {
		t.Fatal("writes should report false on error")
	}
	if cache.Expire(ctx, "k") || cache.ExpireMultiple(ctx, []string{"k"}) {
		t.Fatal("expire should report false on error")
	}
}

func TestTypedCacheHelpers(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()

	if !SetAs(ctx, cache, "dto", dto{ID: 7, Name: "seven"}, time.Minute) {
		t.Fatal("SetAs failed")
	}
	v, ok := GetAs[dto](ctx, cache, "dto")
	if !ok || v.ID != 7 || v.Name != "seven" {
		t.Fatalf("GetAs = %+v, %v", v, ok)
	}
	// C#-style PascalCase JSON decodes into the Go struct.
	cache.Set(ctx, "cs", []byte(`{"Id":9,"Name":"from-csharp"}`), 0)
	if v, ok := GetAs[dto](ctx, cache, "cs"); !ok || v.ID != 9 {
		t.Fatalf("GetAs C# payload = %+v, %v", v, ok)
	}
	cache.Set(ctx, "bad", []byte("not json"), 0)
	if _, ok := GetAs[dto](ctx, cache, "bad"); ok {
		t.Fatal("GetAs should miss on an undecodable value")
	}
	if r := GetWithStatusAs[dto](ctx, cache, "bad"); r.Err == nil || !r.IsKeyExist {
		t.Fatalf("GetWithStatusAs bad = %+v", r)
	}

	got := MultiGetAs[dto](ctx, cache, []string{"dto", "cs", "bad", "missing"})
	if len(got) != 2 || got["cs"].Name != "from-csharp" {
		t.Fatalf("MultiGetAs = %+v", got)
	}
}

// Encode and decode failures in the typed helpers are logged, so schema drift is visible instead of
// looking like a permanent miss.
func TestTypedHelpersLogCodecErrors(t *testing.T) {
	client, _ := testutil.NewClient(t)
	logger, logs := testutil.BufferLogger()
	c := New(client, WithLogger(logger))
	ctx := context.Background()

	c.Set(ctx, "bad", []byte("not json"), 0)
	if _, ok := GetAs[dto](ctx, c, "bad"); ok {
		t.Fatal("GetAs should miss on an undecodable value")
	}
	if !strings.Contains(logs.String(), "decode failed for key: bad") {
		t.Fatalf("decode error not logged; logs: %s", logs.String())
	}
	if SetAs(ctx, c, "fn", func() {}, 0) {
		t.Fatal("SetAs should fail to encode a func")
	}
	if !strings.Contains(logs.String(), "encode failed for key: fn") {
		t.Fatalf("encode error not logged; logs: %s", logs.String())
	}
}

func TestGetOrLoad(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	var replica, primary int32
	load := func(context.Context) (dto, error) {
		atomic.AddInt32(&replica, 1)
		return dto{ID: 1, Name: "replica"}, nil
	}
	critical := func(context.Context) (dto, error) {
		atomic.AddInt32(&primary, 1)
		return dto{ID: 1, Name: "primary"}, nil
	}

	// Miss: load and store.
	v, err := GetOrLoadCritical(ctx, cache, "k", time.Hour, load, critical)
	if err != nil || v.Name != "replica" || replica != 1 {
		t.Fatalf("miss: %+v, %v, replica=%d", v, err, replica)
	}
	if mr.TTL("k") != time.Hour {
		t.Fatalf("loaded value TTL = %v", mr.TTL("k"))
	}
	// Hit: no load.
	if v, _ = GetOrLoadCritical(ctx, cache, "k", time.Hour, load, critical); v.Name != "replica" || replica != 1 {
		t.Fatal("hit should not call the loader")
	}
	// Sentinel: critical loader, then stored.
	cache.ExpireWithCriticalRead(ctx, "k")
	if v, _ = GetOrLoadCritical(ctx, cache, "k", time.Hour, load, critical); v.Name != "primary" || primary != 1 {
		t.Fatalf("sentinel: %+v primary=%d", v, primary)
	}
	if v, ok := GetAs[dto](ctx, cache, "k"); !ok || v.Name != "primary" {
		t.Fatal("critical result should be stored")
	}
	// GetOrLoad without a critical loader uses load for the sentinel.
	cache.ExpireWithCriticalRead(ctx, "k")
	if v, _ = GetOrLoad(ctx, cache, "k", time.Hour, load); v.Name != "replica" {
		t.Fatalf("GetOrLoad sentinel = %+v", v)
	}

	// Loader error: returned and nothing cached.
	boom := errors.New("db down")
	if _, err := GetOrLoad(ctx, cache, "e", time.Hour, func(context.Context) (dto, error) { return dto{}, boom }); !errors.Is(err, boom) {
		t.Fatalf("loader error = %v", err)
	}
	if mr.Exists("e") {
		t.Fatal("a failed load must not be cached")
	}

	// Valkey failing: serve from the loader, do not store.
	mr.SetError("LOADING simulated failure")
	before := replica
	if v, err := GetOrLoad(ctx, cache, "down", time.Hour, load); err != nil || v.Name != "replica" || replica != before+1 {
		t.Fatalf("valkey down: %+v, %v", v, err)
	}
	mr.SetError("")
	if mr.Exists("down") {
		t.Fatal("value loaded while Valkey failed must not be stored")
	}
}

func TestGetOrLoadSingleflight(t *testing.T) {
	cache, _ := newTestCache(t)
	ctx := context.Background()

	var calls int32
	release := make(chan struct{})
	load := func(context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return 42, nil
	}

	const n = 20
	var wg sync.WaitGroup
	results := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = GetOrLoad(ctx, cache, "hot", time.Minute, load)
		}(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("loader called %d times, want 1", got)
	}
	for i, r := range results {
		if r != 42 {
			t.Fatalf("result[%d] = %d", i, r)
		}
	}
}

func TestMultiGetOrLoad(t *testing.T) {
	cache, mr := newTestCache(t)
	ctx := context.Background()

	SetAs(ctx, cache, "hit", dto{ID: 1}, 0)
	cache.ExpireWithCriticalRead(ctx, "crit")
	boom := errors.New("db down")

	got, err := MultiGetOrLoad(ctx, cache, map[string]Callback[dto]{
		"hit":  {Load: func(context.Context) (dto, error) { t.Error("hit must not load"); return dto{}, nil }},
		"miss": {Load: func(context.Context) (dto, error) { return dto{ID: 2}, nil }, TTL: time.Minute},
		"crit": {
			Load:         func(context.Context) (dto, error) { return dto{ID: 30}, nil },
			CriticalLoad: func(context.Context) (dto, error) { return dto{ID: 3}, nil },
			TTL:          time.Minute,
		},
		"fail": {Load: func(context.Context) (dto, error) { return dto{}, boom }},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the loader error", err)
	}
	if got["hit"].ID != 1 || got["miss"].ID != 2 || got["crit"].ID != 3 {
		t.Fatalf("MultiGetOrLoad = %+v", got)
	}
	if _, ok := got["fail"]; ok {
		t.Fatal("failed key should be absent")
	}
	if mr.TTL("miss") != time.Minute {
		t.Fatal("loaded miss should be stored with its TTL")
	}
	if v, ok := GetAs[dto](ctx, cache, "crit"); !ok || v.ID != 3 {
		t.Fatal("critical result should replace the sentinel")
	}
}

func TestNoOpCache(t *testing.T) {
	n := NewNoOp(testutil.DiscardLogger(), nil)
	ctx := context.Background()

	if _, ok := n.Get(ctx, "k"); ok || n.Set(ctx, "k", nil, 0) || n.Exists(ctx, "k") || n.Delete(ctx, "k") {
		t.Fatal("NoOpCache should miss and fail every call")
	}
	if r := n.GetWithStatus(ctx, "k"); !errors.Is(r.Err, ErrNoConnection) {
		t.Fatalf("GetWithStatus err = %v", r.Err)
	}
	var calls int
	v, err := GetOrLoad(ctx, ICache(n), "k", time.Minute, func(context.Context) (string, error) { calls++; return "db", nil })
	if err != nil || v != "db" || calls != 1 {
		t.Fatalf("GetOrLoad via NoOp = %q, %v, calls=%d", v, err, calls)
	}
	got, err := MultiGetOrLoad(ctx, ICache(n), map[string]Callback[string]{
		"a": {Load: func(context.Context) (string, error) { return "A", nil }},
	})
	if err != nil || got["a"] != "A" {
		t.Fatalf("MultiGetOrLoad via NoOp = %v, %v", got, err)
	}
}

func newTestCache(t *testing.T) (ICache, *miniredis.Miniredis) {
	t.Helper()
	client, mr := testutil.NewClient(t)
	return New(client, WithLogger(testutil.DiscardLogger())), mr
}
