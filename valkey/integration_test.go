//go:build integration

// End-to-end tests against a real Valkey (7+). miniredis has no sharded pub/sub, no cluster, no
// ACL users and no TLS, so these cover what the unit tests cannot. Each group runs only when its
// address is set:
//
//	VALKEY_ADDR=host:6379                       standalone suite
//	VALKEY_ADDR=host:7000 VALKEY_CLUSTER=true   cluster suite (adds failover with replicas)
//	+ VALKEY_IAM_USER, VALKEY_CLUSTER_NAME, VALKEY_REGION   run the suite with IAM auth and TLS
//	  against ElastiCache (AWS credentials from the default chain, e.g. AWS_PROFILE)
//	VALKEY_AUTH_ADDR, VALKEY_USERNAME, VALKEY_PASSWORD   ACL user / password auth
//	VALKEY_TLS_ADDR                             TLS (server certificate not verified)
//
// Everything a test writes is under an "e2e:" prefix and deleted when the test ends, and the
// reconnect test kills only its own connections, so the suite is safe on a shared server. Tests the
// server's ACL forbids (NOPERM) are skipped with the reason. The failover test runs CLUSTER FAILOVER:
// point VALKEY_CLUSTER only at a cluster nobody else depends on.
package valkey

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carwale/golibraries/valkey/cache"
	"github.com/carwale/golibraries/valkey/internal/testutil"
	"github.com/carwale/golibraries/valkey/pubsub"
	"github.com/carwale/golibraries/valkey/store"
	valkeygo "github.com/valkey-io/valkey-go"
)

const e2eWait = 5 * time.Second

func clusterMode() bool { return os.Getenv("VALKEY_CLUSTER") == "true" }

func integrationAddr(t *testing.T, env string) string {
	t.Helper()
	addr := os.Getenv(env)
	if addr == "" {
		t.Skip(env + " not set")
	}
	return addr
}

func integrationClient(t *testing.T) *Client {
	t.Helper()
	return integrationClientNamed(t, "")
}

// integrationClientNamed sets CLIENT SETNAME on every connection, so a test can find its own
// connections on a shared server.
func integrationClientNamed(t *testing.T, name string) *Client {
	t.Helper()
	cfg := localConfig(t, integrationAddr(t, "VALKEY_ADDR"))
	cfg.ClientName = name
	if user := os.Getenv("VALKEY_IAM_USER"); user != "" {
		cfg.AuthMode, cfg.TLS = AuthIAM, Bool(true)
		cfg.Username, cfg.ClusterName, cfg.Region = user, os.Getenv("VALKEY_CLUSTER_NAME"), os.Getenv("VALKEY_REGION")
	}
	cfg.IsCluster = Bool(clusterMode())
	cfg.ShardedPubSub = Bool(true) // SSUBSCRIBE works on a single Valkey 7+ node too
	cfg.RequestTimeout = 2 * time.Second
	c, err := NewClient(cfg, WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// prefix returns a unique key prefix so runs never see each other's data, and deletes every key
// under it when the test ends.
func prefix(t *testing.T, c *Client) string {
	p := fmt.Sprintf("e2e:%s:%d", t.Name(), time.Now().UnixNano())
	t.Cleanup(func() { deletePrefix(t, c, p) })
	return p
}

// deletePrefix scans every node for keys containing p (hash-tagged keys put it inside braces) and
// deletes them.
func deletePrefix(t *testing.T, c *Client, p string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var keys []string
	for _, node := range c.Raw().Nodes() {
		var cursor uint64
		for {
			entry, err := node.Do(ctx, node.B().Scan().Cursor(cursor).Match("*"+p+"*").Count(1000).Build()).AsScanEntry()
			if err != nil {
				break // replicas and lost nodes: their primaries are scanned too
			}
			keys = append(keys, entry.Elements...)
			if cursor = entry.Cursor; cursor == 0 {
				break
			}
		}
	}
	if len(keys) == 0 {
		return
	}
	if _, err := c.Store().Del(ctx, keys...); err != nil {
		t.Logf("cleanup of %d keys under %s: %v", len(keys), p, err)
	}
}

// skipIfNoPerm skips the test when the server's ACL forbids the command, so a restricted user
// (for example an ElastiCache service user without @pubsub or @scripting) reports it clearly.
func skipIfNoPerm(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "NOPERM") {
		t.Skipf("the Valkey user lacks permission: %v", err)
	}
}

func ctxWithTimeout(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func pttl(t *testing.T, c *Client, key string) time.Duration {
	t.Helper()
	ms, err := c.Raw().Do(context.Background(), c.Raw().B().Pttl().Key(key).Build()).AsInt64()
	if err != nil {
		t.Fatalf("PTTL %s: %v", key, err)
	}
	return time.Duration(ms) * time.Millisecond
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(e2eWait):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// trySend never blocks, so a callback cannot wedge the dispatcher (and Close) in a failing test.
func trySend[T any](ch chan<- T, v T) {
	select {
	case ch <- v:
	default:
	}
}

func expectNothing[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpected %s: %v", what, v)
	case <-time.After(300 * time.Millisecond):
	}
}

// ---------------------------------------------------------------------------------------------
// Cache

func TestIntegrationCache(t *testing.T) {
	c := integrationClient(t)
	ctx := ctxWithTimeout(t, 30*time.Second)
	p := prefix(t, c)
	ch := c.Cache()

	// Cache-aside miss then hit; the value is stored with its TTL.
	var loads int32
	load := func(context.Context) (string, error) { atomic.AddInt32(&loads, 1); return "replica", nil }
	critical := func(context.Context) (string, error) { return "primary", nil }
	key := p + ":k"
	for i := 0; i < 2; i++ {
		if v, err := cache.GetOrLoadCritical(ctx, ch, key, time.Hour, load, critical); err != nil || v != "replica" {
			t.Fatalf("GetOrLoad #%d = %q, %v", i, v, err)
		}
	}
	if loads != 1 {
		t.Fatalf("loader ran %d times, want 1 (second call is a hit)", loads)
	}
	if ttl := pttl(t, c, key); ttl <= 59*time.Minute {
		t.Fatalf("stored TTL = %v, want about 1h", ttl)
	}

	// Critical read: sentinel, then the primary loader replaces it.
	if !ch.ExpireWithCriticalRead(ctx, key) {
		t.Fatal("ExpireWithCriticalRead failed")
	}
	if r := ch.GetWithStatus(ctx, key); !r.IsCriticalRead {
		t.Fatalf("GetWithStatus = %+v, want critical read", r)
	}
	if v, _ := cache.GetOrLoadCritical(ctx, ch, key, time.Hour, load, critical); v != "primary" {
		t.Fatalf("critical load = %q", v)
	}

	// A write during a miss load wins (SET NX).
	race := p + ":race"
	v, err := cache.GetOrLoad(ctx, ch, race, time.Hour, func(context.Context) (string, error) {
		ch.ExpireWithCriticalRead(ctx, race)
		return "stale", nil
	})
	if err != nil || v != "stale" {
		t.Fatalf("GetOrLoad = %q, %v", v, err)
	}
	if r := ch.GetWithStatus(ctx, race); !r.IsCriticalRead {
		t.Fatalf("stale load overwrote the sentinel: %+v", r)
	}

	// Soft expire: PEXPIRE LT never extends a shorter TTL, and gives a key without one the refresh TTL.
	short, forever := p+":short", p+":forever"
	ch.Set(ctx, short, []byte("v"), 2*time.Second)
	ch.Set(ctx, forever, []byte("v"), 0)
	if !ch.Expire(ctx, short) || !ch.Expire(ctx, forever) {
		t.Fatal("Expire failed")
	}
	if ttl := pttl(t, c, short); ttl > 2*time.Second || ttl <= 0 {
		t.Fatalf("short key TTL after soft expire = %v, want <= 2s", ttl)
	}
	if ttl := pttl(t, c, forever); ttl > 10*time.Second || ttl < 9*time.Second {
		t.Fatalf("key without TTL after soft expire = %v, want about 10s", ttl)
	}

	// Multi-key operations across slots (no hash tags).
	keys := []string{p + ":a", p + ":b", p + ":c", p + ":d", p + ":e"}
	for i, k := range keys {
		if !ch.Set(ctx, k, []byte(strconv.Itoa(i)), time.Hour) {
			t.Fatalf("Set %s failed", k)
		}
	}
	if got := ch.MultiGet(ctx, keys); len(got) != len(keys) {
		t.Fatalf("MultiGet returned %d of %d keys", len(got), len(keys))
	}
	if !ch.ExpireMultiple(ctx, keys) {
		t.Fatal("ExpireMultiple across slots failed")
	}
	if ttl := pttl(t, c, keys[0]); ttl > 10*time.Second {
		t.Fatalf("ExpireMultiple TTL = %v, want <= 10s", ttl)
	}
	callbacks := map[string]cache.Callback[string]{}
	for _, k := range append(keys, p+":m1", p+":m2") {
		k := k
		callbacks[k] = cache.Callback[string]{Load: func(context.Context) (string, error) { return "loaded:" + k, nil }, TTL: time.Minute}
	}
	all, err := cache.MultiGetOrLoad(ctx, ch, callbacks)
	if err != nil || len(all) != len(callbacks) || all[p+":m1"] != "loaded:"+p+":m1" {
		t.Fatalf("MultiGetOrLoad = %v, %v", all, err)
	}
	if !ch.ExpireMultipleWithoutDelay(ctx, append(keys, p+":m1", p+":m2")) || ch.Exists(ctx, p+":m1") {
		t.Fatal("ExpireMultipleWithoutDelay across slots failed")
	}
}

// ---------------------------------------------------------------------------------------------
// Store

// closeScript is the chat gateway's close script; its keys share the {user} hash tag.
var e2eCloseScript = store.NewScript(`
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

func TestIntegrationStore(t *testing.T) {
	c := integrationClient(t)
	ctx := ctxWithTimeout(t, 30*time.Second)
	p := prefix(t, c)
	s := c.Store()

	k := p + ":k"
	if err := s.Set(ctx, k, []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetNX(ctx, k, []byte("other"), 0); err != nil || ok {
		t.Fatalf("SetNX on an existing key = %v, %v", ok, err)
	}
	if _, err := s.Expire(ctx, k, 0); !errors.Is(err, store.ErrInvalidTTL) {
		t.Fatalf("Expire(0) err = %v, want ErrInvalidTTL", err)
	}
	if v, err := s.Get(ctx, k); err != nil || string(v) != "v" {
		t.Fatalf("Get after rejected Expire = %q, %v", v, err)
	}
	if ok, err := s.Persist(ctx, k); err != nil || !ok {
		t.Fatalf("Persist = %v, %v", ok, err)
	}
	if ttl, err := s.TTL(ctx, k); err != nil || ttl != store.NoExpiry {
		t.Fatalf("TTL after Persist = %v, %v", ttl, err)
	}
	if n, err := s.IncrBy(ctx, p+":n", 5); err != nil || n != 5 {
		t.Fatalf("IncrBy = %d, %v", n, err)
	}

	// Hashes, sets, sorted sets.
	h := p + ":h"
	if _, err := s.HSet(ctx, h, map[string][]byte{"a": []byte("1"), "b": []byte("2")}); err != nil {
		t.Fatal(err)
	}
	if all, err := s.HGetAll(ctx, h); err != nil || len(all) != 2 {
		t.Fatalf("HGetAll = %v, %v", all, err)
	}
	if _, err := s.HGet(ctx, h, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("HGet missing err = %v", err)
	}
	if _, err := s.SAdd(ctx, p+":s", "x", "y"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SIsMember(ctx, p+":s", "y"); err != nil || !ok {
		t.Fatalf("SIsMember = %v, %v", ok, err)
	}
	z := p + ":z"
	if _, err := s.ZAdd(ctx, z, map[string]float64{"a": 1, "b": 2, "c": 3}); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ZRangeByScore(ctx, z, "-inf", "+inf", 1, 0); err != nil || len(r) != 2 || r[0].Member != "b" {
		t.Fatalf("ZRangeByScore offset 1, no limit = %+v, %v", r, err)
	}

	// Cross-slot Del / Exists / MGet.
	keys := []string{p + ":x1", p + ":x2", p + ":x3", p + ":x4"}
	for _, key := range keys {
		_ = s.Set(ctx, key, []byte("1"), time.Minute)
	}
	if got, err := s.MGet(ctx, keys); err != nil || len(got) != len(keys) {
		t.Fatalf("MGet = %d keys, %v", len(got), err)
	}
	if n, err := s.Exists(ctx, keys...); err != nil || n != int64(len(keys)) {
		t.Fatalf("Exists = %d, %v", n, err)
	}
	if n, err := s.Del(ctx, keys...); err != nil || n != int64(len(keys)) {
		t.Fatalf("Del = %d, %v", n, err)
	}

}

func TestIntegrationStoreScripts(t *testing.T) {
	c := integrationClient(t)
	ctx := ctxWithTimeout(t, 30*time.Second)
	p := prefix(t, c)
	s := c.Store()

	// Lua script with hash-tagged keys (one slot in a cluster).
	tag := "{" + p + ":user}"
	presence, lastseen := "presence:"+tag, "lastseen:"+tag
	_, _ = s.HSet(ctx, presence, map[string][]byte{"c1": []byte("podA|web"), "c2": []byte("podB|ios")})
	n, err := s.RunScriptInt64(ctx, e2eCloseScript, []string{presence, lastseen}, []string{"c1", "1700000000", "podA", "podB"})
	skipIfNoPerm(t, err)
	if err != nil || n != 0 {
		t.Fatalf("close script with a live device = %d, %v", n, err)
	}
	if n, err := s.RunScriptInt64(ctx, e2eCloseScript, []string{presence, lastseen}, []string{"c2", "1700000001", "podA", "podB"}); err != nil || n != 1 {
		t.Fatalf("close script for the last device = %d, %v", n, err)
	}
	if v, err := s.Get(ctx, lastseen); err != nil || string(v) != "1700000001" {
		t.Fatalf("lastseen = %q, %v", v, err)
	}
	if v, err := s.RunScript(ctx, store.NewScript("return nil"), []string{presence}, nil); v != nil || err != nil {
		t.Fatalf("nil script reply = %v, %v", v, err)
	}
	if v, err := s.RunScriptStrings(ctx, store.NewScript("return {KEYS[1], ARGV[1]}"), []string{presence}, []string{"x"}); err != nil || len(v) != 2 || v[1] != "x" {
		t.Fatalf("RunScriptStrings = %v, %v", v, err)
	}
	if _, err := s.RunScript(ctx, store.NewScript("return redis.call('NOPE')"), []string{presence}, nil); err == nil {
		t.Fatal("script error should be returned")
	}
}

// ---------------------------------------------------------------------------------------------
// Pub/sub

func TestIntegrationShardedPubSub(t *testing.T) {
	c := integrationClient(t)
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx := ctxWithTimeout(t, 30*time.Second)

	// Channels without hash tags land in different slots (different shards in a cluster).
	p := prefix(t, c)
	channels := []string{p + ":ch:pod:a", p + ":ch:pod:b", p + ":ch:pod:c", p + ":ch:pod:d"}
	got := make(chan pubsub.Message, 10)
	s, err := sub.Subscribe(ctx, channels, func(m pubsub.Message) { got <- m })
	skipIfNoPerm(t, err)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	msgs := make([]pubsub.OutboundMessage, len(channels))
	for i, ch := range channels {
		msgs[i] = pubsub.OutboundMessage{Channel: ch, Payload: []byte(ch)}
	}
	for i, err := range c.Publisher().PublishMulti(ctx, msgs) {
		if err != nil {
			t.Fatalf("SPUBLISH %d: %v", i, err)
		}
	}
	seen := map[string]bool{}
	for len(seen) < len(channels) {
		m := waitFor(t, got, fmt.Sprintf("sharded messages (have %v)", seen))
		if m.Channel != string(m.Payload) {
			t.Fatalf("message %+v delivered on the wrong channel", m)
		}
		seen[m.Channel] = true
	}

	// After Close the server no longer has the subscription.
	s.Close()
	if n, err := c.Publisher().Publish(ctx, channels[0], []byte("late")); err != nil || n != 0 {
		t.Fatalf("SPUBLISH after Close reached %d subscribers, %v", n, err)
	}
}

func TestIntegrationClassicAndPatternPubSub(t *testing.T) {
	c := integrationClient(t)
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx := ctxWithTimeout(t, 30*time.Second)
	p := prefix(t, c)

	classic := make(chan pubsub.Message, 10)
	_, err = sub.Subscribe(ctx, []string{p + ":classic"}, func(m pubsub.Message) { classic <- m }, pubsub.WithSharded(false))
	skipIfNoPerm(t, err)
	if err != nil {
		t.Fatalf("classic Subscribe: %v", err)
	}
	if _, err := c.Publisher().Publish(ctx, p+":classic", []byte("1"), pubsub.WithClassicPublish()); err != nil {
		t.Fatal(err)
	}
	if m := waitFor(t, classic, "classic message"); string(m.Payload) != "1" {
		t.Fatalf("classic message = %+v", m)
	}

	pattern := make(chan pubsub.Message, 10)
	if _, err := sub.PSubscribe(ctx, []string{p + ":ch:pod:*"}, func(m pubsub.Message) { pattern <- m }); err != nil {
		t.Fatalf("PSubscribe: %v", err)
	}
	if _, err := c.Publisher().Publish(ctx, p+":ch:pod:x", []byte("2"), pubsub.WithClassicPublish()); err != nil {
		t.Fatal(err)
	}
	m := waitFor(t, pattern, "pattern message")
	if m.Channel != p+":ch:pod:x" || m.Pattern != p+":ch:pod:*" || string(m.Payload) != "2" {
		t.Fatalf("pattern message = %+v", m)
	}

	// PUBLISH and SPUBLISH are separate namespaces: pattern subscribers never see SPUBLISH, and
	// a classic subscriber never sees a sharded publish.
	if _, err := c.Publisher().Publish(ctx, p+":ch:pod:y", []byte("sharded")); err != nil {
		t.Fatal(err)
	}
	expectNothing(t, pattern, "pattern message from SPUBLISH")
	if _, err := c.Publisher().Publish(ctx, p+":classic", []byte("sharded")); err != nil {
		t.Fatal(err)
	}
	expectNothing(t, classic, "classic message from SPUBLISH")
}

// Classic and pattern commands carry no slot, so a cluster client would send SUBSCRIBE and the later
// UNSUBSCRIBE to arbitrary nodes. After closing, no node may still hold one of this test's
// subscriptions (sub= and psub= on its own connections, found by client name).
func TestIntegrationUnsubscribeReachesTheSubscribedNode(t *testing.T) {
	name := fmt.Sprintf("e2e-unsub-%d", time.Now().UnixNano())
	c := integrationClientNamed(t, name)
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx := ctxWithTimeout(t, 60*time.Second)
	p := prefix(t, c)

	// Several rounds: with random routing, a mismatch is near certain on a multi-node cluster.
	for i := 0; i < 10; i++ {
		classic, err := sub.Subscribe(ctx, []string{fmt.Sprintf("%s:unsub:%d", p, i)}, func(pubsub.Message) {}, pubsub.WithSharded(false))
		skipIfNoPerm(t, err)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		pattern, err := sub.PSubscribe(ctx, []string{fmt.Sprintf("%s:unsub:%d:*", p, i)}, func(pubsub.Message) {})
		if err != nil {
			t.Fatalf("PSubscribe: %v", err)
		}
		classic.Close()
		pattern.Close()
	}

	seen := 0
	for _, addr := range nodeAddrs(t, c) {
		node := nodeClient(t, c, addr)
		// Every type: a connection whose last subscription ended may no longer count as PUBSUB.
		list, err := node.Do(ctx, node.B().Arbitrary("CLIENT", "LIST").Build()).ToString()
		skipIfNoPerm(t, err) // CLIENT LIST is an admin command; service users rarely have it
		if err != nil {
			t.Fatalf("CLIENT LIST on %s: %v", addr, err)
		}
		for _, counts := range subCountsNamed(list, name) {
			seen++
			if counts != "sub=0 psub=0" {
				t.Errorf("%s still holds subscriptions after Close: %s", addr, counts)
			}
		}
	}
	if seen == 0 {
		t.Fatal("found none of this test's connections; the check did not run")
	}
	t.Logf("checked %d of this test's connections", seen)
}

// subCountsNamed returns "sub=N psub=M" for each CLIENT LIST entry whose name is exactly name.
func subCountsNamed(list, name string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimPrefix(list, "txt:"), "\n") {
		var sub, psub string
		match := false
		for _, f := range strings.Fields(line) {
			switch {
			case f == "name="+name:
				match = true
			case strings.HasPrefix(f, "sub="):
				sub = f
			case strings.HasPrefix(f, "psub="):
				psub = f
			}
		}
		if match {
			out = append(out, sub+" "+psub)
		}
	}
	return out
}

// A user without @pubsub (common for ElastiCache service users) must get NOPERM from Subscribe at
// once, not after the confirmation timeout, and no OnDisconnected for a subscription that never ran.
func TestIntegrationSubscribeRejectionFailsFast(t *testing.T) {
	c := integrationClient(t)
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx := ctxWithTimeout(t, 30*time.Second)

	var disconnects atomic.Int32
	start := time.Now()
	s, err := sub.Subscribe(ctx, []string{prefix(t, c) + ":ch"}, func(pubsub.Message) {},
		pubsub.OnDisconnected(func(error) { disconnects.Add(1) }))
	took := time.Since(start)
	if err == nil {
		s.Close()
		t.Skip("this user may subscribe; nothing to reject")
	}
	if !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("Subscribe = %v, want NOPERM", err)
	}
	if took > 3*time.Second || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Subscribe took %v (%v); a rejection should fail fast", took, err)
	}
	if n := disconnects.Load(); n != 0 {
		t.Fatalf("OnDisconnected called %d times for a subscription that never started", n)
	}
	t.Logf("rejected in %v: %v", took.Round(time.Millisecond), err)
}

// clientIDsNamed returns the ids of the CLIENT LIST entries whose name is exactly name.
func clientIDsNamed(list, name string) []string {
	var ids []string
	// RESP3 sends CLIENT LIST as a verbatim string; valkey-go keeps its "txt:" format prefix.
	list = strings.TrimPrefix(list, "txt:")
	for _, line := range strings.Split(list, "\n") {
		var id string
		match := false
		for _, f := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(f, "id="); ok {
				id = v
			}
			if f == "name="+name {
				match = true
			}
		}
		if match && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// nodeAddrs returns every node (masters and replicas) of the server under test.
func nodeAddrs(t *testing.T, c *Client) []string {
	t.Helper()
	if !clusterMode() {
		return []string{integrationAddr(t, "VALKEY_ADDR")}
	}
	var addrs []string
	for _, n := range clusterNodes(t, c) {
		addrs = append(addrs, n.addr)
	}
	return addrs
}

type clusterNode struct {
	id, addr, masterID string
	master             bool
	slots              [][2]int
}

func clusterNodes(t *testing.T, c *Client) []clusterNode {
	t.Helper()
	out, err := c.Raw().Do(context.Background(), c.Raw().B().Arbitrary("CLUSTER", "NODES").Build()).ToString()
	if err != nil {
		t.Fatalf("CLUSTER NODES: %v", err)
	}
	var nodes []clusterNode
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || strings.Contains(f[2], "fail") || strings.Contains(f[2], "handshake") {
			continue
		}
		n := clusterNode{id: f[0], addr: strings.SplitN(strings.SplitN(f[1], "@", 2)[0], ",", 2)[0], masterID: f[3], master: strings.Contains(f[2], "master")}
		for _, r := range f[8:] {
			if strings.HasPrefix(r, "[") {
				continue // slot being migrated
			}
			lo, hi, _ := strings.Cut(r, "-")
			a, _ := strconv.Atoi(lo)
			b := a
			if hi != "" {
				b, _ = strconv.Atoi(hi)
			}
			n.slots = append(n.slots, [2]int{a, b})
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// nodeClient connects to one node with the client's own settings (TLS, auth), not as a cluster.
func nodeClient(t *testing.T, c *Client, addr string) valkeygo.Client {
	t.Helper()
	opt := c.option
	opt.InitAddress = []string{addr}
	opt.ForceSingleClient = true
	return testutil.Connect(t, opt)
}

// A dropped subscriber connection must be resubscribed, report its lifecycle, and deliver again.
// Only this test's own connections are killed (found by client name): the server may be shared.
func TestIntegrationResubscribeAfterConnectionKill(t *testing.T) {
	name := fmt.Sprintf("e2e-resub-%d", time.Now().UnixNano())
	c := integrationClientNamed(t, name)
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx := ctxWithTimeout(t, 60*time.Second)
	p := prefix(t, c)
	channel := p + ":ch:pod:resub"

	got := make(chan pubsub.Message, 10)
	disconnected := make(chan error, 10)
	resubscribed := make(chan string, 10)
	s, err := sub.Subscribe(ctx, []string{channel}, func(m pubsub.Message) { got <- m },
		pubsub.OnDisconnected(func(err error) { trySend(disconnected, err) }),
		pubsub.OnResubscribed(func(ch string) { trySend(resubscribed, ch) }),
		pubsub.WithBackoff(50*time.Millisecond, 500*time.Millisecond))
	skipIfNoPerm(t, err)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()

	killed := 0
	for _, addr := range nodeAddrs(t, c) {
		node := nodeClient(t, c, addr)
		list, err := node.Do(ctx, node.B().Arbitrary("CLIENT", "LIST", "TYPE", "PUBSUB").Build()).ToString()
		skipIfNoPerm(t, err) // CLIENT LIST / KILL are admin commands; service users rarely have them
		if err != nil {
			t.Fatalf("CLIENT LIST on %s: %v", addr, err)
		}
		for _, id := range clientIDsNamed(list, name) {
			err := node.Do(ctx, node.B().Arbitrary("CLIENT", "KILL", "ID", id).Build()).Error()
			skipIfNoPerm(t, err)
			if err != nil {
				t.Fatalf("CLIENT KILL ID %s on %s: %v", id, addr, err)
			}
			killed++
		}
	}
	if killed == 0 {
		t.Fatal("found none of this test's pub/sub connections to kill; the test did not exercise a drop")
	}
	t.Logf("killed %d of this test's own pub/sub connections", killed)
	waitFor(t, disconnected, "OnDisconnected")
	if ch := waitFor(t, resubscribed, "OnResubscribed"); ch != channel {
		t.Fatalf("OnResubscribed(%q), want %q", ch, channel)
	}
	if _, err := c.Publisher().Publish(ctx, channel, []byte("after")); err != nil {
		t.Fatal(err)
	}
	if m := waitFor(t, got, "message after resubscribe"); string(m.Payload) != "after" {
		t.Fatalf("message = %+v", m)
	}
}

// A manual failover moves the channel's slot to a new primary. The sharded subscription must follow
// it: the old primary drops it (SUNSUBSCRIBE), the subscriber resubscribes on the new one.
func TestIntegrationShardedSubscriptionSurvivesFailover(t *testing.T) {
	if !clusterMode() {
		t.Skip("cluster only")
	}
	c := integrationClient(t)
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx := ctxWithTimeout(t, 90*time.Second)
	channel := prefix(t, c) + ":ch:pod:failover"

	slot, err := c.Raw().Do(ctx, c.Raw().B().Arbitrary("CLUSTER", "KEYSLOT", channel).Build()).AsInt64()
	if err != nil {
		t.Fatalf("CLUSTER KEYSLOT: %v", err)
	}
	nodes := clusterNodes(t, c)
	var primary clusterNode
	for _, n := range nodes {
		for _, r := range n.slots {
			if n.master && int(slot) >= r[0] && int(slot) <= r[1] {
				primary = n
			}
		}
	}
	var replica clusterNode
	for _, n := range nodes {
		if !n.master && n.masterID == primary.id {
			replica = n
		}
	}
	if primary.id == "" || replica.id == "" {
		t.Skipf("slot %d has no primary with a replica (nodes: %+v)", slot, nodes)
	}

	got := make(chan pubsub.Message, 10)
	var disconnects atomic.Int32
	resubscribed := make(chan string, 10)
	s, err := sub.Subscribe(ctx, []string{channel}, func(m pubsub.Message) { got <- m },
		pubsub.OnDisconnected(func(error) { disconnects.Add(1) }),
		pubsub.OnResubscribed(func(ch string) { trySend(resubscribed, ch) }),
		pubsub.WithBackoff(100*time.Millisecond, time.Second))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer s.Close()
	if _, err := c.Publisher().Publish(ctx, channel, []byte("before")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, got, "message before failover")

	rc := nodeClient(t, c, replica.addr)
	if err := rc.Do(ctx, rc.B().Arbitrary("CLUSTER", "FAILOVER").Build()).Error(); err != nil {
		t.Fatalf("CLUSTER FAILOVER on %s: %v", replica.addr, err)
	}
	// Wait until the replica reports itself as primary.
	deadline := time.Now().Add(30 * time.Second)
	for {
		role, _ := rc.Do(ctx, rc.B().Arbitrary("ROLE").Build()).ToArray()
		if len(role) > 0 {
			if r, _ := role[0].ToString(); r == "master" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not become primary", replica.addr)
		}
		time.Sleep(200 * time.Millisecond)
	}

	select {
	case ch := <-resubscribed:
		if ch != channel {
			t.Fatalf("OnResubscribed(%q), want %q", ch, channel)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("no resubscribe after failover (disconnects seen: %d)", disconnects.Load())
	}

	// Publish until delivered: the publisher's topology also has to catch up with the failover.
	deadline = time.Now().Add(30 * time.Second)
	for {
		if _, err := c.Publisher().Publish(ctx, channel, []byte("after")); err == nil {
			select {
			case m := <-got:
				if string(m.Payload) != "after" {
					t.Fatalf("message = %+v", m)
				}
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no message delivered on the new primary")
		}
	}
}

// Many slow lifecycle callbacks must not stall the connection on a real server either.
func TestIntegrationSlowCallbacks(t *testing.T) {
	c := integrationClient(t)
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx := ctxWithTimeout(t, 60*time.Second)
	p := prefix(t, c)

	channels := make([]string, 80)
	for i := range channels {
		channels[i] = fmt.Sprintf("%s:ch:%d", p, i)
	}
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	got := make(chan pubsub.Message, 1)
	s, err := sub.Subscribe(ctx, channels, func(m pubsub.Message) { got <- m },
		pubsub.OnSubscribed(func(string) { <-release }))
	skipIfNoPerm(t, err)
	if err != nil {
		t.Fatalf("Subscribe with blocked callbacks: %v", err)
	}
	if _, err := c.Publisher().Publish(ctx, channels[len(channels)-1], []byte("x")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, got, "message while callbacks are blocked")
	once.Do(func() { close(release) })
	s.Close()
}

// ---------------------------------------------------------------------------------------------
// Auth and TLS

func TestIntegrationPasswordAuth(t *testing.T) {
	addr := integrationAddr(t, "VALKEY_AUTH_ADDR")
	user, pass := os.Getenv("VALKEY_USERNAME"), os.Getenv("VALKEY_PASSWORD")
	ctx := ctxWithTimeout(t, 30*time.Second)

	cfg := localConfig(t, addr)
	cfg.AuthMode, cfg.Username, cfg.Password = AuthPassword, user, pass
	c, err := NewClient(cfg, WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("NewClient with the right password: %v", err)
	}
	defer c.Close()
	k := prefix(t, c)
	if err := c.Store().Set(ctx, k, []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set as %s: %v", user, err)
	}

	// The subscriber authenticates with the same credentials on its own connections.
	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	got := make(chan pubsub.Message, 1)
	if _, err := sub.Subscribe(ctx, []string{k + ":ch"}, func(m pubsub.Message) { got <- m }); err != nil {
		t.Fatalf("Subscribe as %s: %v", user, err)
	}
	if _, err := c.Publisher().Publish(ctx, k+":ch", []byte("x")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, got, "message on an authenticated subscriber")

	bad := localConfig(t, addr)
	bad.AuthMode, bad.Username, bad.Password = AuthPassword, user, "wrong"
	if c, err := NewClient(bad, WithLogger(discardLogger())); err == nil {
		c.Close()
		t.Fatal("NewClient with a wrong password should fail")
	}
	if c, err := NewClient(localConfig(t, addr), WithLogger(discardLogger())); err == nil {
		c.Close()
		t.Fatal("NewClient without auth should fail when the default user is disabled")
	}
}

func TestIntegrationTLS(t *testing.T) {
	addr := integrationAddr(t, "VALKEY_TLS_ADDR")
	ctx := ctxWithTimeout(t, 30*time.Second)

	cfg := localConfig(t, addr)
	cfg.TLS, cfg.InsecureSkipVerify = Bool(true), true
	c, err := NewClient(cfg, WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("NewClient over TLS: %v", err)
	}
	defer c.Close()
	k := prefix(t, c)
	if err := c.Store().Set(ctx, k, []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set over TLS: %v", err)
	}
	if v, err := c.Store().Get(ctx, k); err != nil || string(v) != "v" {
		t.Fatalf("Get over TLS = %q, %v", v, err)
	}

	sub, err := c.NewSubscriber()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	got := make(chan pubsub.Message, 1)
	if _, err := sub.Subscribe(ctx, []string{k + ":ch"}, func(m pubsub.Message) { got <- m }); err != nil {
		t.Fatalf("Subscribe over TLS: %v", err)
	}
	if _, err := c.Publisher().Publish(ctx, k+":ch", []byte("x")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, got, "message over TLS")

	plain := localConfig(t, addr)
	if c, err := NewClient(plain, WithLogger(discardLogger())); err == nil {
		c.Close()
		t.Fatal("a plaintext client should not connect to the TLS port")
	}
}
