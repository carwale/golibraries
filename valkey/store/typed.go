package store

import (
	"context"
	"fmt"
	"time"
)

// GetAs reads key and decodes it into T. It returns ErrNotFound for a missing key and a
// wrapped error when the value does not decode.
func GetAs[T any](ctx context.Context, s IStore, key string) (T, error) {
	var v T
	raw, err := s.Get(ctx, key)
	if err != nil {
		return v, err
	}
	if err := s.Transcoder().Unmarshal(raw, &v); err != nil {
		var zero T
		return zero, fmt.Errorf("valkey: decoding key %q: %w", key, err)
	}
	return v, nil
}

// SetAs encodes value and stores it. A ttl of 0 or less stores it without expiry.
func SetAs[T any](ctx context.Context, s IStore, key string, value T, ttl time.Duration) error {
	raw, err := s.Transcoder().Marshal(value)
	if err != nil {
		return fmt.Errorf("valkey: encoding key %q: %w", key, err)
	}
	return s.Set(ctx, key, raw, ttl)
}

// HGetAs reads one hash field and decodes it into T. It returns ErrNotFound for a missing field.
func HGetAs[T any](ctx context.Context, s IStore, key, field string) (T, error) {
	var v T
	raw, err := s.HGet(ctx, key, field)
	if err != nil {
		return v, err
	}
	if err := s.Transcoder().Unmarshal(raw, &v); err != nil {
		var zero T
		return zero, fmt.Errorf("valkey: decoding %q field %q: %w", key, field, err)
	}
	return v, nil
}

// HSetAs encodes value and stores it in one hash field. It reports whether the field was new.
func HSetAs[T any](ctx context.Context, s IStore, key, field string, value T) (bool, error) {
	raw, err := s.Transcoder().Marshal(value)
	if err != nil {
		return false, fmt.Errorf("valkey: encoding %q field %q: %w", key, field, err)
	}
	n, err := s.HSet(ctx, key, map[string][]byte{field: raw})
	return n > 0, err
}
