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
)

// NoExpiry is returned by IStore.TTL for a key that exists without a TTL.
const NoExpiry time.Duration = -1

// ZMember is a sorted-set member with its score.
type ZMember struct {
	Member string
	Score  float64
}

// IStore is the system-of-record API. Reads of a missing key, field or member return ErrNotFound.
//
// A ttl of 0 or less means no expiry. Typed access is provided by GetAs, SetAs, HGetAs and HSetAs.
type IStore interface {
	// Strings and keys
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// SetNX sets the key only if it does not exist and reports whether it was set.
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// MGet returns the keys that exist; missing keys are omitted. Cross-slot keys are allowed.
	MGet(ctx context.Context, keys []string) (map[string][]byte, error)
	// Del deletes keys (cross-slot allowed) and returns how many existed.
	Del(ctx context.Context, keys ...string) (int64, error)
	// Exists returns how many of the keys exist (cross-slot allowed).
	Exists(ctx context.Context, keys ...string) (int64, error)
	// Expire sets a TTL and reports whether the key exists.
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
	// Valkey syntax ("-inf", "+inf", "(5" for exclusive). count <= 0 means no limit.
	ZRangeByScore(ctx context.Context, key, min, max string, offset, count int64) ([]ZMember, error)
	ZCard(ctx context.Context, key string) (int64, error)

	// Scripts
	// RunScript runs s with keys and args. The error is also available as result.Error().
	RunScript(ctx context.Context, s *Script, keys, args []string) (valkeygo.ValkeyResult, error)

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
	nx := s.client.B().Set().Key(key).Value(valkeygo.BinaryString(value)).Nx()
	var cmd valkeygo.Completed
	if ttl > 0 {
		cmd = nx.PxMilliseconds(common.DurationMillis(ttl)).Build()
	} else {
		cmd = nx.Build()
	}
	err := s.do(ctx, cmd).Error()
	if valkeygo.IsValkeyNil(err) {
		return false, nil // key already existed
	}
	return err == nil, err
}

func (s *valkeyStore) MGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	unique := common.UniqueKeys(keys)
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
	unique := common.UniqueKeys(keys)
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
		n, err := s.Del(ctx, key)
		return n > 0, err
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
	if count > 0 {
		cmd = base.Limit(offset, count).Build()
	} else {
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

func (s *valkeyStore) RunScript(ctx context.Context, script *Script, keys, args []string) (valkeygo.ValkeyResult, error) {
	if script == nil || script.lua == nil {
		return valkeygo.ValkeyResult{}, errors.New("valkey: RunScript needs a script")
	}
	ctx, cancel := common.WithTimeout(ctx, s.timeout)
	defer cancel()
	r := script.lua.Exec(ctx, s.client, keys, args)
	return r, r.Error()
}
