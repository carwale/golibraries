// Package store uses Valkey as the system of record for ephemeral state (presence, counters,
// tokens, Lua scripts). Unlike the cache package, every error is returned, nothing is swallowed,
// and the critical-read sentinel has no special meaning.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/carwale/golibraries/valkey/internal/common"
	"github.com/carwale/golibraries/valkey/transcoder"
	valkeygo "github.com/valkey-io/valkey-go"
)

var (
	// ErrNotFound is returned by IStore reads when the key, field or member does not exist.
	ErrNotFound = errors.New("valkey: not found")
	// ErrEmptyKey is returned for an empty or blank key.
	ErrEmptyKey = common.ErrEmptyKey
	// ErrInvalidTTL is returned by Expire for a ttl of 0 or less. Use Persist to remove a TTL and
	// Del to delete a key.
	ErrInvalidTTL = errors.New("valkey: Expire needs a positive ttl; use Persist to remove a TTL or Del to delete")
)

// NoExpiry is returned by IStore.TTL for a key that exists without a TTL.
const NoExpiry time.Duration = -1

// ZMember is a sorted-set member with its score.
type ZMember struct {
	Member string
	Score  float64
}

// IStore is the system-of-record API. Single-value reads (Get, HGet, ZScore, TTL) return
// ErrNotFound for a missing key, field or member. Batch reads (MGet, HMGet) omit missing items and
// return no error. Every key must be non-blank: a blank key, also inside MGet, Del or Exists, is
// rejected with ErrEmptyKey.
//
// In Set and SetNX a ttl of 0 or less stores without expiry; Expire rejects it with ErrInvalidTTL.
// Typed access is provided by GetAs, SetAs, HGetAs and HSetAs.
//
// Multi-key commands that allow keys in different cluster slots (MGet, Del, Exists) are not atomic:
// they run one command per slot or key. On a partial failure Del and Exists return the count of the
// commands that succeeded with the joined errors, so the caller cannot tell which keys were affected;
// retry them one key at a time, or keep related keys in one slot with a {hash tag} and use a script.
type IStore interface {
	// Strings and keys
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// SetNX sets the key only if it does not exist and reports whether it was set.
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// MGet returns the keys that exist; missing keys are omitted. Cross-slot keys are allowed.
	MGet(ctx context.Context, keys []string) (map[string][]byte, error)
	// Del deletes keys (cross-slot allowed, not atomic) and returns how many existed.
	Del(ctx context.Context, keys ...string) (int64, error)
	// Exists returns how many of the keys exist (cross-slot allowed, not atomic).
	Exists(ctx context.Context, keys ...string) (int64, error)
	// Expire sets a TTL and reports whether the key exists. ttl must be positive (ErrInvalidTTL).
	Expire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// TTL returns the remaining TTL, NoExpiry for a key without one, or ErrNotFound.
	TTL(ctx context.Context, key string) (time.Duration, error)
	// Persist removes the TTL and reports whether one was removed.
	Persist(ctx context.Context, key string) (bool, error)
	Incr(ctx context.Context, key string) (int64, error)
	IncrBy(ctx context.Context, key string, delta int64) (int64, error)

	// Hashes
	// HSet sets the fields and returns how many were newly created.
	HSet(ctx context.Context, key string, fields map[string][]byte) (int64, error)
	HGet(ctx context.Context, key, field string) ([]byte, error)
	// HMGet returns the requested fields that exist; missing fields are omitted.
	HMGet(ctx context.Context, key string, fields ...string) (map[string][]byte, error)
	// HGetAll returns every field; an empty map when the key does not exist.
	HGetAll(ctx context.Context, key string) (map[string][]byte, error)
	HDel(ctx context.Context, key string, fields ...string) (int64, error)
	HExists(ctx context.Context, key, field string) (bool, error)
	HLen(ctx context.Context, key string) (int64, error)
	HIncrBy(ctx context.Context, key, field string, delta int64) (int64, error)

	// Sets
	SAdd(ctx context.Context, key string, members ...string) (int64, error)
	SRem(ctx context.Context, key string, members ...string) (int64, error)
	SMembers(ctx context.Context, key string) ([]string, error)
	SIsMember(ctx context.Context, key, member string) (bool, error)
	SCard(ctx context.Context, key string) (int64, error)

	// Sorted sets
	// ZAdd adds or updates members and returns how many were newly added.
	ZAdd(ctx context.Context, key string, members map[string]float64) (int64, error)
	ZRem(ctx context.Context, key string, members ...string) (int64, error)
	ZScore(ctx context.Context, key, member string) (float64, error)
	// ZRangeByScore returns members with scores in [min, max], ordered by score. min and max use
	// Valkey syntax ("-inf", "+inf", "(5" for exclusive). offset skips that many members; count <= 0
	// means no limit.
	ZRangeByScore(ctx context.Context, key, min, max string, offset, count int64) ([]ZMember, error)
	ZCard(ctx context.Context, key string) (int64, error)

	// Scripts. In cluster mode every key a script touches must be in one slot.
	// RunScript runs s and returns its reply decoded: int64, string, float64, bool, []any or
	// map[string]any. A nil reply (Lua false or nil) returns nil, nil.
	RunScript(ctx context.Context, s *Script, keys, args []string) (any, error)
	// RunScriptInt64 runs s and returns its integer reply.
	RunScriptInt64(ctx context.Context, s *Script, keys, args []string) (int64, error)
	// RunScriptString runs s and returns its string reply; ErrNotFound for a nil reply.
	RunScriptString(ctx context.Context, s *Script, keys, args []string) (string, error)
	// RunScriptStrings runs s and returns its array reply as strings.
	RunScriptStrings(ctx context.Context, s *Script, keys, args []string) ([]string, error)

	// Transcoder returns the encoder used by the typed helpers.
	Transcoder() transcoder.ITranscoder
}

type valkeyStore struct {
	client     valkeygo.Client
	timeout    time.Duration
	transcoder transcoder.ITranscoder
}

var _ IStore = (*valkeyStore)(nil)

// Option customises the store returned by New.
type Option func(s *valkeyStore)

// WithTranscoder replaces the default JSON transcoder used by the typed helpers.
func WithTranscoder(t transcoder.ITranscoder) Option {
	return func(s *valkeyStore) {
		if t != nil {
			s.transcoder = t
		}
	}
}

// WithRequestTimeout bounds each command when the caller's context has no deadline. Default 250ms.
func WithRequestTimeout(d time.Duration) Option {
	return func(s *valkeyStore) {
		if d > 0 {
			s.timeout = d
		}
	}
}

// New returns an IStore on an existing valkey-go client. Most services get it from
// valkey.Client.Store instead.
func New(client valkeygo.Client, opts ...Option) IStore {
	s := &valkeyStore{client: client, timeout: common.DefaultRequestTimeout, transcoder: transcoder.JSON{}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *valkeyStore) Transcoder() transcoder.ITranscoder { return s.transcoder }

func (s *valkeyStore) do(ctx context.Context, cmd valkeygo.Completed) valkeygo.ValkeyResult {
	ctx, cancel := common.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.client.Do(ctx, cmd)
}

func checkKey(key string) error {
	if common.Blank(key) {
		return ErrEmptyKey
	}
	return nil
}

// uniqueKeys rejects any blank key with ErrEmptyKey, so a bug that produces an empty key is not
// mistaken for "key does not exist", and drops duplicates.
func uniqueKeys(keys []string) ([]string, error) {
	for _, k := range keys {
		if err := checkKey(k); err != nil {
			return nil, err
		}
	}
	return common.UniqueKeys(keys), nil
}

// notFound maps a Valkey nil reply to ErrNotFound.
func notFound(err error) error {
	if valkeygo.IsValkeyNil(err) {
		return ErrNotFound
	}
	return err
}

func (s *valkeyStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	v, err := s.do(ctx, s.client.B().Get().Key(key).Build()).AsBytes()
	return v, notFound(err)
}

func (s *valkeyStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := checkKey(key); err != nil {
		return err
	}
	return s.do(ctx, common.SetCmd(s.client, key, value, ttl)).Error()
}

func (s *valkeyStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}
	err := s.do(ctx, common.SetNXCmd(s.client, key, value, ttl)).Error()
	if valkeygo.IsValkeyNil(err) {
		return false, nil // key already existed
	}
	return err == nil, err
}

func (s *valkeyStore) MGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	unique, err := uniqueKeys(keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(unique))
	if len(unique) == 0 {
		return out, nil
	}
	ctx, cancel := common.WithTimeout(ctx, s.timeout)
	defer cancel()
	msgs, err := valkeygo.MGet(s.client, ctx, unique)
	if err != nil {
		return nil, err
	}
	for k, m := range msgs {
		if m.IsNil() {
			continue
		}
		v, err := m.AsBytes()
		if err != nil {
			if valkeygo.IsValkeyNil(err) {
				continue
			}
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

// perKey pipelines one single-key command per key and sums the integer replies, so keys in
// different cluster slots are fine.
func (s *valkeyStore) perKey(ctx context.Context, keys []string, build func(string) valkeygo.Completed) (int64, error) {
	unique, err := uniqueKeys(keys)
	if err != nil {
		return 0, err
	}
	if len(unique) == 0 {
		return 0, nil
	}
	ctx, cancel := common.WithTimeout(ctx, s.timeout)
	defer cancel()
	cmds := make(valkeygo.Commands, 0, len(unique))
	for _, k := range unique {
		cmds = append(cmds, build(k))
	}
	var total int64
	var errs []error
	for _, r := range s.client.DoMulti(ctx, cmds...) {
		n, err := r.AsInt64()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		total += n
	}
	return total, errors.Join(errs...)
}

func (s *valkeyStore) Del(ctx context.Context, keys ...string) (int64, error) {
	return s.perKey(ctx, keys, func(k string) valkeygo.Completed { return s.client.B().Del().Key(k).Build() })
}

func (s *valkeyStore) Exists(ctx context.Context, keys ...string) (int64, error) {
	return s.perKey(ctx, keys, func(k string) valkeygo.Completed { return s.client.B().Exists().Key(k).Build() })
}

func (s *valkeyStore) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}
	if ttl <= 0 {
		return false, ErrInvalidTTL
	}
	return s.do(ctx, s.client.B().Pexpire().Key(key).Milliseconds(common.DurationMillis(ttl)).Build()).AsBool()
}

func (s *valkeyStore) TTL(ctx context.Context, key string) (time.Duration, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	ms, err := s.do(ctx, s.client.B().Pttl().Key(key).Build()).AsInt64()
	switch {
	case err != nil:
		return 0, err
	case ms == -2:
		return 0, ErrNotFound
	case ms == -1:
		return NoExpiry, nil
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (s *valkeyStore) Persist(ctx context.Context, key string) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}
	return s.do(ctx, s.client.B().Persist().Key(key).Build()).AsBool()
}

func (s *valkeyStore) Incr(ctx context.Context, key string) (int64, error) {
	return s.IncrBy(ctx, key, 1)
}

func (s *valkeyStore) IncrBy(ctx context.Context, key string, delta int64) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	return s.do(ctx, s.client.B().Incrby().Key(key).Increment(delta).Build()).AsInt64()
}

func (s *valkeyStore) HSet(ctx context.Context, key string, fields map[string][]byte) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	if len(fields) == 0 {
		return 0, nil
	}
	cmd := s.client.B().Hset().Key(key).FieldValue()
	for f, v := range fields {
		cmd = cmd.FieldValue(f, valkeygo.BinaryString(v))
	}
	return s.do(ctx, cmd.Build()).AsInt64()
}

func (s *valkeyStore) HGet(ctx context.Context, key, field string) ([]byte, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	v, err := s.do(ctx, s.client.B().Hget().Key(key).Field(field).Build()).AsBytes()
	return v, notFound(err)
}

func (s *valkeyStore) HMGet(ctx context.Context, key string, fields ...string) (map[string][]byte, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(fields))
	if len(fields) == 0 {
		return out, nil
	}
	msgs, err := s.do(ctx, s.client.B().Hmget().Key(key).Field(fields...).Build()).ToArray()
	if err != nil {
		return nil, err
	}
	for i, m := range msgs {
		if i >= len(fields) || m.IsNil() {
			continue
		}
		v, err := m.AsBytes()
		if err != nil {
			return nil, err
		}
		out[fields[i]] = v
	}
	return out, nil
}

func (s *valkeyStore) HGetAll(ctx context.Context, key string) (map[string][]byte, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	m, err := s.do(ctx, s.client.B().Hgetall().Key(key).Build()).AsStrMap()
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(m))
	for f, v := range m {
		out[f] = []byte(v)
	}
	return out, nil
}

func (s *valkeyStore) HDel(ctx context.Context, key string, fields ...string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	if len(fields) == 0 {
		return 0, nil
	}
	return s.do(ctx, s.client.B().Hdel().Key(key).Field(fields...).Build()).AsInt64()
}

func (s *valkeyStore) HExists(ctx context.Context, key, field string) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}
	return s.do(ctx, s.client.B().Hexists().Key(key).Field(field).Build()).AsBool()
}

func (s *valkeyStore) HLen(ctx context.Context, key string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	return s.do(ctx, s.client.B().Hlen().Key(key).Build()).AsInt64()
}

func (s *valkeyStore) HIncrBy(ctx context.Context, key, field string, delta int64) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	return s.do(ctx, s.client.B().Hincrby().Key(key).Field(field).Increment(delta).Build()).AsInt64()
}

func (s *valkeyStore) SAdd(ctx context.Context, key string, members ...string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	if len(members) == 0 {
		return 0, nil
	}
	return s.do(ctx, s.client.B().Sadd().Key(key).Member(members...).Build()).AsInt64()
}

func (s *valkeyStore) SRem(ctx context.Context, key string, members ...string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	if len(members) == 0 {
		return 0, nil
	}
	return s.do(ctx, s.client.B().Srem().Key(key).Member(members...).Build()).AsInt64()
}

func (s *valkeyStore) SMembers(ctx context.Context, key string) ([]string, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	return s.do(ctx, s.client.B().Smembers().Key(key).Build()).AsStrSlice()
}

func (s *valkeyStore) SIsMember(ctx context.Context, key, member string) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}
	return s.do(ctx, s.client.B().Sismember().Key(key).Member(member).Build()).AsBool()
}

func (s *valkeyStore) SCard(ctx context.Context, key string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	return s.do(ctx, s.client.B().Scard().Key(key).Build()).AsInt64()
}

func (s *valkeyStore) ZAdd(ctx context.Context, key string, members map[string]float64) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	if len(members) == 0 {
		return 0, nil
	}
	cmd := s.client.B().Zadd().Key(key).ScoreMember()
	for m, score := range members {
		cmd = cmd.ScoreMember(score, m)
	}
	return s.do(ctx, cmd.Build()).AsInt64()
}

func (s *valkeyStore) ZRem(ctx context.Context, key string, members ...string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	if len(members) == 0 {
		return 0, nil
	}
	return s.do(ctx, s.client.B().Zrem().Key(key).Member(members...).Build()).AsInt64()
}

func (s *valkeyStore) ZScore(ctx context.Context, key, member string) (float64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	v, err := s.do(ctx, s.client.B().Zscore().Key(key).Member(member).Build()).AsFloat64()
	return v, notFound(err)
}

func (s *valkeyStore) ZRangeByScore(ctx context.Context, key, min, max string, offset, count int64) ([]ZMember, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	base := s.client.B().Zrangebyscore().Key(key).Min(min).Max(max).Withscores()
	var cmd valkeygo.Completed
	switch {
	case count > 0:
		cmd = base.Limit(offset, count).Build()
	case offset > 0:
		cmd = base.Limit(offset, -1).Build() // a negative count means all remaining members
	default:
		cmd = base.Build()
	}
	scores, err := s.do(ctx, cmd).AsZScores()
	if err != nil {
		return nil, err
	}
	out := make([]ZMember, len(scores))
	for i, z := range scores {
		out[i] = ZMember{Member: z.Member, Score: z.Score}
	}
	return out, nil
}

func (s *valkeyStore) ZCard(ctx context.Context, key string) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	return s.do(ctx, s.client.B().Zcard().Key(key).Build()).AsInt64()
}

var errNoScript = errors.New("valkey: RunScript needs a script")

func (s *valkeyStore) runScript(ctx context.Context, script *Script, keys, args []string) (valkeygo.ValkeyResult, error) {
	if script == nil || script.lua == nil {
		return valkeygo.ValkeyResult{}, errNoScript
	}
	ctx, cancel := common.WithTimeout(ctx, s.timeout)
	defer cancel()
	r := script.lua.Exec(ctx, s.client, keys, args)
	return r, r.Error()
}

func (s *valkeyStore) RunScript(ctx context.Context, script *Script, keys, args []string) (any, error) {
	r, err := s.runScript(ctx, script, keys, args)
	if valkeygo.IsValkeyNil(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.ToAny()
}

func (s *valkeyStore) RunScriptInt64(ctx context.Context, script *Script, keys, args []string) (int64, error) {
	r, err := s.runScript(ctx, script, keys, args)
	if err != nil {
		return 0, notFound(err)
	}
	return r.AsInt64()
}

func (s *valkeyStore) RunScriptString(ctx context.Context, script *Script, keys, args []string) (string, error) {
	r, err := s.runScript(ctx, script, keys, args)
	if err != nil {
		return "", notFound(err)
	}
	return r.ToString()
}

func (s *valkeyStore) RunScriptStrings(ctx context.Context, script *Script, keys, args []string) ([]string, error) {
	r, err := s.runScript(ctx, script, keys, args)
	if err != nil {
		return nil, notFound(err)
	}
	return r.AsStrSlice()
}
