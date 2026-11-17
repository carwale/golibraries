package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/carwale/golibraries/valkey/internal/testutil"
)

func TestStoreStringsAndKeys(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing err = %v, want ErrNotFound", err)
	}
	if err := s.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(ctx, "k"); err != nil || string(v) != "v" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if ttl, err := s.TTL(ctx, "k"); err != nil || ttl != time.Minute {
		t.Fatalf("TTL = %v, %v", ttl, err)
	}
	if ok, err := s.Persist(ctx, "k"); err != nil || !ok {
		t.Fatalf("Persist = %v, %v", ok, err)
	}
	if ttl, _ := s.TTL(ctx, "k"); ttl != NoExpiry {
		t.Fatalf("TTL after Persist = %v, want NoExpiry", ttl)
	}
	if _, err := s.TTL(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TTL missing err = %v", err)
	}
	if ok, err := s.Expire(ctx, "k", 5*time.Second); err != nil || !ok || mr.TTL("k") != 5*time.Second {
		t.Fatalf("Expire = %v, %v, ttl %v", ok, err, mr.TTL("k"))
	}
	if ok, _ := s.Expire(ctx, "missing", time.Second); ok {
		t.Fatal("Expire on a missing key should report false")
	}

	if ok, err := s.SetNX(ctx, "lock", []byte("a"), time.Second); err != nil || !ok {
		t.Fatalf("SetNX first = %v, %v", ok, err)
	}
	if ok, err := s.SetNX(ctx, "lock", []byte("b"), time.Second); err != nil || ok {
		t.Fatalf("SetNX second = %v, %v", ok, err)
	}
	if v, _ := s.Get(ctx, "lock"); string(v) != "a" {
		t.Fatal("SetNX must not overwrite")
	}

	if n, err := s.IncrBy(ctx, "ctr", 5); err != nil || n != 5 {
		t.Fatalf("IncrBy = %d, %v", n, err)
	}
	if n, _ := s.Incr(ctx, "ctr"); n != 6 {
		t.Fatalf("Incr = %d", n)
	}

	_ = s.Set(ctx, "a", []byte("1"), 0)
	_ = s.Set(ctx, "b", []byte("2"), 0)
	m, err := s.MGet(ctx, []string{"a", "b", "missing"})
	if err != nil || len(m) != 2 || string(m["b"]) != "2" {
		t.Fatalf("MGet = %v, %v", m, err)
	}
	if n, err := s.Exists(ctx, "a", "b", "missing"); err != nil || n != 2 {
		t.Fatalf("Exists = %d, %v", n, err)
	}
	if n, err := s.Del(ctx, "a", "b", "missing"); err != nil || n != 2 {
		t.Fatalf("Del = %d, %v", n, err)
	}

	if err := s.Set(ctx, "", []byte("v"), 0); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Set blank key err = %v", err)
	}
}

func TestStoreHashes(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	key := "presence:{carwale:u_8f21}"

	n, err := s.HSet(ctx, key, map[string][]byte{"c_1": []byte("podA|web"), "c_2": []byte("podB|ios")})
	if err != nil || n != 2 {
		t.Fatalf("HSet = %d, %v", n, err)
	}
	if v, err := s.HGet(ctx, key, "c_1"); err != nil || string(v) != "podA|web" {
		t.Fatalf("HGet = %q, %v", v, err)
	}
	if _, err := s.HGet(ctx, key, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("HGet missing err = %v", err)
	}
	m, err := s.HMGet(ctx, key, "c_2", "nope")
	if err != nil || len(m) != 1 || string(m["c_2"]) != "podB|ios" {
		t.Fatalf("HMGet = %v, %v", m, err)
	}
	all, err := s.HGetAll(ctx, key)
	if err != nil || len(all) != 2 {
		t.Fatalf("HGetAll = %v, %v", all, err)
	}
	if empty, err := s.HGetAll(ctx, "missing"); err != nil || len(empty) != 0 {
		t.Fatalf("HGetAll missing = %v, %v", empty, err)
	}
	if ok, _ := s.HExists(ctx, key, "c_1"); !ok {
		t.Fatal("HExists c_1")
	}
	if l, _ := s.HLen(ctx, key); l != 2 {
		t.Fatalf("HLen = %d", l)
	}
	if n, _ := s.HDel(ctx, key, "c_1", "nope"); n != 1 {
		t.Fatalf("HDel = %d", n)
	}
	if n, _ := s.HIncrBy(ctx, "counts", "x", 3); n != 3 {
		t.Fatalf("HIncrBy = %d", n)
	}

	isNew, err := HSetAs(ctx, s, "h", "f", dto{ID: 5})
	if err != nil || !isNew {
		t.Fatalf("HSetAs = %v, %v", isNew, err)
	}
	if v, err := HGetAs[dto](ctx, s, "h", "f"); err != nil || v.ID != 5 {
		t.Fatalf("HGetAs = %+v, %v", v, err)
	}
}

func TestStoreSetsAndSortedSets(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if n, _ := s.SAdd(ctx, "s", "a", "b", "a"); n != 2 {
		t.Fatalf("SAdd = %d", n)
	}
	if ok, _ := s.SIsMember(ctx, "s", "b"); !ok {
		t.Fatal("SIsMember b")
	}
	if n, _ := s.SCard(ctx, "s"); n != 2 {
		t.Fatalf("SCard = %d", n)
	}
	if n, _ := s.SRem(ctx, "s", "a"); n != 1 {
		t.Fatalf("SRem = %d", n)
	}
	if m, _ := s.SMembers(ctx, "s"); len(m) != 1 || m[0] != "b" {
		t.Fatalf("SMembers = %v", m)
	}

	if n, _ := s.ZAdd(ctx, "z", map[string]float64{"a": 1, "b": 2, "c": 3}); n != 3 {
		t.Fatalf("ZAdd = %d", n)
	}
	if sc, err := s.ZScore(ctx, "z", "b"); err != nil || sc != 2 {
		t.Fatalf("ZScore = %v, %v", sc, err)
	}
	if _, err := s.ZScore(ctx, "z", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ZScore missing err = %v", err)
	}
	r, err := s.ZRangeByScore(ctx, "z", "(1", "+inf", 0, 0)
	if err != nil || len(r) != 2 || r[0].Member != "b" || r[1].Score != 3 {
		t.Fatalf("ZRangeByScore = %+v, %v", r, err)
	}
	if r, _ = s.ZRangeByScore(ctx, "z", "-inf", "+inf", 1, 1); len(r) != 1 || r[0].Member != "b" {
		t.Fatalf("ZRangeByScore limit = %+v", r)
	}
	if n, _ := s.ZRem(ctx, "z", "a"); n != 1 {
		t.Fatalf("ZRem = %d", n)
	}
	if n, _ := s.ZCard(ctx, "z"); n != 2 {
		t.Fatalf("ZCard = %d", n)
	}
}

// closeScript mirrors the chat gateway's close script: drop the connection field and, if no field
// is left on a live pod, write lastseen. Live pods are passed as arguments (cluster-safe).
var closeScript = NewScript(`
redis.call('HDEL', KEYS[1], ARGV[1])
local live = {}
for i = 3, #ARGV do live[ARGV[i]] = true end
local fields = redis.call('HGETALL', KEYS[1])
for i = 2, #fields, 2 do
  local pod = string.match(fields[i], '^([^|]+)')
  if live[pod] then return 0 end
end
redis.call('SET', KEYS[2], ARGV[2])
return 1
`)

func TestStoreRunScript(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()
	presence, lastseen := "presence:{t:u}", "lastseen:{t:u}"

	_, _ = s.HSet(ctx, presence, map[string][]byte{"c1": []byte("podA|web"), "c2": []byte("podB|ios")})

	res, err := s.RunScript(ctx, closeScript, []string{presence, lastseen}, []string{"c1", "1700000000", "podA", "podB"})
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	if n, _ := res.AsInt64(); n != 0 || mr.Exists(lastseen) {
		t.Fatal("another live device remains: lastseen must not be written")
	}
	res, _ = s.RunScript(ctx, closeScript, []string{presence, lastseen}, []string{"c2", "1700000001", "podA", "podB"})
	if n, _ := res.AsInt64(); n != 1 {
		t.Fatal("last device closed: script should write lastseen")
	}
	if v, _ := mr.Get(lastseen); v != "1700000001" {
		t.Fatalf("lastseen = %q", v)
	}

	if _, err := s.RunScript(ctx, NewScript("return redis.call('NOPE')"), nil, nil); err == nil {
		t.Fatal("script errors should be returned")
	}
	if _, err := s.RunScript(ctx, nil, nil, nil); err == nil {
		t.Fatal("nil script should fail")
	}
}

func TestStoreTypedAndErrors(t *testing.T) {
	s, mr := newTestStore(t)
	ctx := context.Background()

	if err := SetAs(ctx, s, "obj", dto{ID: 3, Name: "x"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if v, err := GetAs[dto](ctx, s, "obj"); err != nil || v.Name != "x" {
		t.Fatalf("GetAs = %+v, %v", v, err)
	}
	if _, err := GetAs[dto](ctx, s, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAs missing err = %v", err)
	}
	_ = s.Set(ctx, "bad", []byte("nope"), 0)
	if _, err := GetAs[dto](ctx, s, "bad"); err == nil {
		t.Fatal("GetAs should fail to decode")
	}

	mr.SetError("LOADING simulated failure")
	if err := s.Set(ctx, "k", []byte("v"), 0); err == nil {
		t.Fatal("store errors must be returned")
	}
	if _, err := s.HSet(ctx, "h", map[string][]byte{"f": nil}); err == nil {
		t.Fatal("store errors must be returned")
	}
	if _, err := s.Del(ctx, "a", "b"); err == nil {
		t.Fatal("store errors must be returned")
	}
}

type dto struct {
	ID   int    `json:"Id"`
	Name string `json:"Name"`
}

func newTestStore(t *testing.T) (IStore, *miniredis.Miniredis) {
	t.Helper()
	client, mr := testutil.NewClient(t)
	return New(client), mr
}
