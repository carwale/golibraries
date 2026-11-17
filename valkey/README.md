# valkey

Valkey client for Go services, built on [valkey-go](https://github.com/valkey-io/valkey-go) (pure Go, no CGO).
It is the Go counterpart of the C# `AEPLCore.Valkey` library, plus two things the C# library does not have:

| API | Use it for | Errors |
|---|---|---|
| `client.Cache()` → `cache.ICache` | Cache-aside in front of a database. Same semantics as `AEPLCore.Valkey`. | Logged and swallowed: a failing cache becomes misses. |
| `client.Store()` → `store.IStore` | Valkey as the system of record for ephemeral state: presence, counters, tokens, Lua scripts. | Always returned. |
| `client.Publisher()` / `client.NewSubscriber()` → `pubsub` | Classic, sharded (`SPUBLISH`/`SSUBSCRIBE`) and pattern (`PSUBSCRIBE`) pub/sub, with automatic resubscribe. | Returned; drops reported through callbacks. |

Authentication defaults to ElastiCache RBAC/IAM: tokens are generated with SigV4 and refreshed by the library.
Password and no-auth modes exist for local development.

## Package layout

```
valkey/                 Client, Config, Option            ← start here
├── cache/              ICache, NoOp, GetOrLoad and other typed cache-aside helpers
├── store/              IStore, Script, typed helpers
├── pubsub/             IPublisher, ISubscriber, subscription options
├── transcoder/         ITranscoder, JSON (default)
└── internal/
    ├── common/         shared helpers (timeouts, key checks)
    ├── iam/            ElastiCache IAM token signing
    └── testutil/       miniredis helpers for tests
```

| C# (`AEPLCore.Valkey`) | Go |
|---|---|
| `Config/ValkeyCacheConfig.cs` | `valkey.Config` |
| `Extension/ServiceCollectionExtension.cs` | `valkey.NewClient` |
| `Interfaces/IValkeyCache.cs`, `Valkey/ValkeyCache.cs` | `cache.ICache`, `cache.New` |
| `Valkey/NoOpValkeyCache.cs` | `cache.NoOp`, `cache.NewNoOp` |
| `Entities/CacheResult.cs` | `cache.Result`, `cache.TypedResult[T]` |
| `Entities/ValkeyCacheCallback.cs` | `cache.Callback[T]` |
| `Interfaces/ITranscoder.cs`, `Transcoder/JsonTranscoder.cs` | `transcoder.ITranscoder`, `transcoder.JSON` |
| — | `store`, `pubsub` (new) |

## Setup

```go
import (
    "github.com/carwale/golibraries/valkey"
    "github.com/carwale/golibraries/valkey/cache"
)

client, err := valkey.NewClient(valkey.Config{
    Host:        "master.my-cache.aps1.cache.amazonaws.com",
    IsCluster:     valkey.Bool(false), // required: true for ElastiCache cluster mode
    ShardedPubSub: valkey.Bool(false), // required: must match every other service on your channels
    Username:      "my-iam-user",      // IAM user attached to the cache
    ClusterName:   "my-cache",         // lowercased automatically
    Region:        "ap-south-1",
}, valkey.WithLogger(logger))
if err != nil {
    // Keep running without a cache, as the C# library does:
    c := cache.NewNoOp(logger, nil)
}
defer client.Close()
```

| Config field | Default | Notes |
|---|---|---|
| `Host` | — | Required. Primary or configuration endpoint. |
| `IsCluster` | — | Required, no default: guessing wrong aims a standalone client at a cluster. |
| `Port` | `6379` | |
| `TLS` | `true` | IAM auth requires TLS. |
| `AuthMode` | `AuthIAM` | `AuthPassword` (with `Username`/`Password`) or `AuthNone` for local Valkey. |
| `Username`, `ClusterName`, `Region` | — | Required for IAM. `Serverless: true` for ElastiCache Serverless. |
| `RequestTimeout` | `250ms` | Applied when the caller's context has no deadline. |
| `RefreshCacheDuration` | `10s` | TTL for soft expire and the critical-read sentinel. |
| `LoadTimeout` | `5s` | Bounds a `GetOrLoad` load shared by concurrent callers (see below). |
| `ShardedPubSub` | — | Required, no default. `SPUBLISH`/`SSUBSCRIBE` instead of `PUBLISH`/`SUBSCRIBE` (Valkey 7+). The two are separate namespaces: every publisher and subscriber of a channel must agree, and C# services (StackExchange.Redis) publish classic by default. |

IAM credentials come from the AWS default chain (IRSA, ECS/EC2 role, environment). Pass `valkey.WithAWSCredentials`
or `valkey.WithAWSConfig` to override. The service role needs `elasticache:Connect` on the cache and user.

Local Valkey:

```go
valkey.NewClient(valkey.Config{Host: "localhost", IsCluster: valkey.Bool(false), ShardedPubSub: valkey.Bool(false),
    TLS: valkey.Bool(false), AuthMode: valkey.AuthNone})
```

## Cache (`valkey/cache`)

Port of `IValkeyCache`. `ICache` methods operate on bytes; typed access is through generic functions that work on any
`ICache` (including mocks and `cache.NoOp`).

```go
c := client.Cache()

// Typed get / set
dto, ok := cache.GetAs[MyDto](ctx, c, "foo:42")
cache.SetAs(ctx, c, "foo:42", dto, 2*time.Hour)

// Cache-aside: hit → decode; miss → load, store, return
dto, err := cache.GetOrLoad(ctx, c, "foo:42", 2*time.Hour,
    func(ctx context.Context) (MyDto, error) { return db.GetFoo(ctx, 42) })

// Cache-aside with critical read (master-replica databases)
c.ExpireWithCriticalRead(ctx, "foo:42")                       // on write
dto, err := cache.GetOrLoadCritical(ctx, c, "foo:42", 2*time.Hour,
    replicaLoader, primaryLoader)                               // on read

// Soft expire: value stays readable for at most RefreshCacheDuration, then expires
c.Expire(ctx, "foo:42")
c.ExpireWithDelay(ctx, "foo:42", 30*time.Second)
c.ExpireWithoutDelay(ctx, "foo:42")                           // hard delete

// Multi-get: one round trip per slot
hits := cache.MultiGetAs[MyDto](ctx, c, []string{"k1", "k2"})
all, err := cache.MultiGetOrLoad(ctx, c, map[string]cache.Callback[MyDto]{
    "k1": {Load: loadK1, TTL: time.Hour},
    "k2": {Load: loadK2, CriticalLoad: loadK2Primary, TTL: time.Hour},
}, cache.WithMaxConcurrentLoads(8)) // default 8
```

| C# (`IValkeyCache`) | Go |
|---|---|
| `GetAsync(key)` / `SetAsync(key, value[, expiry])` | `c.Get` / `c.Set(ctx, key, value, ttl)` (ttl 0 = no expiry) |
| `GetAsync<T>` / `SetAsync<T>` | `cache.GetAs[T]` / `cache.SetAs[T]` |
| `GetAsync<T>(key, dbCallback, expiry[, dbCriticalCallback])` | `cache.GetOrLoad[T]` / `cache.GetOrLoadCritical[T]` |
| `GetWithStatusAsync<T>` | `c.GetWithStatus` / `cache.GetWithStatusAs[T]` |
| `DeleteAsync` / `ExistsAsync` | `c.Delete` / `c.Exists` |
| `ExpireAsync` / `ExpireWithDelayAsync` / `ExpireWithoutDelayAsync` | `c.Expire` / `c.ExpireWithDelay` / `c.ExpireWithoutDelay` |
| `ExpireMultipleAsync` / `ExpireMultipleWithoutDelayAsync` | `c.ExpireMultiple` / `c.ExpireMultipleWithoutDelay` |
| `ExpireWithCriticalReadAsync` | `c.ExpireWithCriticalRead` |
| `MultiGetAsync<T>(keys)` / `MultiGetAsync<T>(callbacks)` | `cache.MultiGetAs[T]` / `cache.MultiGetOrLoad[T]` |

Behaviour notes:

- Errors are logged and treated as misses. When Valkey is unreachable, `GetOrLoad` calls the loader and returns its
  value **without** writing it back. A loader error is returned and nothing is cached; a loader panic is returned as
  an error.
- After a miss, the loaded value is stored with `SET NX`: a write (or `ExpireWithCriticalRead`) that lands while a
  slow replica read is in flight wins over the stale result. The critical path overwrites the sentinel.
- Concurrent `GetOrLoad` calls for the same key and type in one process share a single load, also while Valkey is
  down. The load is detached from any one caller's cancellation and bounded by `LoadTimeout` or the first caller's
  deadline, whichever is later; each caller stops waiting when its own context ends. Callers sharing a load get the
  same value, so treat maps, slices and pointers as read-only.
- `MultiGetOrLoad` runs at most 8 loaders at once (`cache.WithMaxConcurrentLoads`).
- The critical-read sentinel is `_valkey_critical_` (`cache.CriticalSentinel`), the same as the C# library, so Go and
  C# services can share keys.
- Soft expire uses `PEXPIRE ... LT` (Valkey 7+): it never extends a TTL that is already shorter, so invalidating
  after a write cannot keep stale data longer. The C# library re-writes the value with the refresh TTL, which can.
- Values are JSON by default. `System.Text.Json` writes PascalCase property names, so give Go structs shared with C#
  matching `json` tags. Replace the encoding with `valkey.WithTranscoder`.

## Store (`valkey/store`)

Valkey as the source of truth. No sentinel handling, nothing swallowed; reads of a missing key, field or member return
`store.ErrNotFound`. In `Set`/`SetNX` a ttl of 0 means no expiry; `Expire` rejects it with `store.ErrInvalidTTL`
(use `Persist` or `Del` to say what you mean).

```go
s := client.Store()

// Strings, counters, keys
s.Set(ctx, "revoked:{carwale:u_8f21}", []byte("1762240000"), 65*time.Minute)
ok, err := s.SetNX(ctx, "lock:job", []byte(podID), 30*time.Second)
n, err := s.Incr(ctx, "counter")

// Hashes
s.HSet(ctx, "presence:{carwale:u_8f21}", map[string][]byte{"c_9a3e": []byte("podA|web|1762240000")})
fields, err := s.HGetAll(ctx, "presence:{carwale:u_8f21}")

// Sets and sorted sets
s.SAdd(ctx, "dealer:agents", "a_17")
members, err := s.ZRangeByScore(ctx, "scores", "-inf", "+inf", 0, 0) // offset, count (0 = no limit)

// Typed
err = store.SetAs(ctx, s, "session:1", session, time.Hour)
session, err := store.GetAs[Session](ctx, s, "session:1")

// Lua scripts (EVALSHA with EVAL fallback). Create once, reuse.
var closeScript = store.NewScript(`...`)
n, err := s.RunScriptInt64(ctx, closeScript, []string{presenceKey, lastSeenKey}, args)
// also RunScript (decoded any), RunScriptString, RunScriptStrings
```

In cluster mode every key a script touches must be in one slot: use `{hash tags}`. `MGet`, `Del` and `Exists`
accept keys in different slots but are **not atomic**: they run per slot or per key, and on a partial failure `Del`
and `Exists` return the count that succeeded with the joined errors, without saying which keys failed. Retry key by
key, or keep related keys in one slot and use a script when you need all-or-nothing.

## Pub/sub (`valkey/pubsub`)

```go
// Publish (SPUBLISH when ShardedPubSub, PUBLISH otherwise)
n, err := client.Publisher().Publish(ctx, "ch:pod:podA", payload)

// Fan out in one round trip per node
errs := client.Publisher().PublishMulti(ctx, []pubsub.OutboundMessage{
    {Channel: "ch:pod:podA", Payload: eventA},
    {Channel: "ch:pod:podB", Payload: eventB},
})

// Subscribe on a dedicated connection set
sub, err := client.NewSubscriber()
defer sub.Close()

s, err := sub.Subscribe(ctx, []string{"ch:pod:" + podID}, handle,
    pubsub.OnSubscribed(func(ch string) { /* first confirmation */ }),
    pubsub.OnDisconnected(func(err error) { /* messages are being lost */ }),
    pubsub.OnResubscribed(func(ch string) { startPacedResync() }),
    pubsub.WithBackoff(100*time.Millisecond, 5*time.Second))
// Subscribe returns once Valkey has confirmed every channel (or ctx ends), so readiness can wait on it.
defer s.Close()

// Pattern subscription ("observe" every matching channel)
p, err := sub.PSubscribe(ctx, []string{"ch:pod:*"}, func(m pubsub.Message) {
    log.Printf("%s via %s: %s", m.Channel, m.Pattern, m.Payload)
})
```

Things to know:

- Pub/sub is at-most-once. While the subscription connection is down, messages are lost; `OnDisconnected` and
  `OnResubscribed` tell you when to catch up from the source of truth.
- The handler runs on the connection's read goroutine, in publish order. It must not block. Lifecycle callbacks run
  one at a time on a separate goroutine, from an unbounded queue, so a slow `OnResubscribed` (a paced resync) never
  stalls message delivery or the connection.
- Don't call `Close` from a handler or callback (it would wait for itself); use `go s.Close()`.
- In sharded mode each channel gets its own receive loop and `SSUBSCRIBE` round trip. Keep the channel count per
  subscription small (one per pod, not one per user).
- `Subscribe` readiness is all or nothing: if any channel is not confirmed in time, the subscription is closed and an
  error returned. Subscribe channels that can fail independently in separate calls.
- Valkey has no sharded pattern subscription. `PSubscribe` is always classic and does **not** see `SPUBLISH`
  messages; in a cluster, publish channels you want to observe by pattern with `pubsub.WithClassicPublish()`.
- `ISubscription.Close` sends `UNSUBSCRIBE`/`SUNSUBSCRIBE`/`PUNSUBSCRIBE`, waits for the loops to stop and for a
  running callback to return; callbacks still queued are dropped.
  `ISubscriber.Close` closes every subscription and its connections; `Client.Close` does not close subscribers.

## Using the packages without `valkey.Client`

Each package also has a constructor that takes a valkey-go client you created yourself:
`cache.New(raw, cache.WithLogger(l))`, `store.New(raw)`, `pubsub.NewPublisher(raw, pubsub.WithShardedPublish(true))`
and `pubsub.NewSubscriber(raw)`. A subscriber's client must be used for nothing else and created with
`DisableRetry: true`, or drops are retried silently inside valkey-go and the callbacks never fire.

## Escape hatches

`client.Raw()` returns the underlying `valkey.Client` for commands not wrapped here, and
`valkey.WithValkeyOption(func(*valkeygo.ClientOption))` adjusts the valkey-go options (for example `ConnLifetime`).

## Testing

Unit tests run against [miniredis](https://github.com/alicebob/miniredis):

```
go test ./valkey/...
```

Sharded pub/sub, cross-slot reads and IAM auth are not covered by miniredis. Run the integration tests against a
real Valkey 7+ (ideally a cluster) before relying on them:

```
VALKEY_ADDR=localhost:6379 go test -tags integration ./valkey/...
VALKEY_ADDR=localhost:7000 VALKEY_CLUSTER=true go test -tags integration ./valkey/...
```
