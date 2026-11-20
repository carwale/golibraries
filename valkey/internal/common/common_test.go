package common

import (
	"context"
	"testing"
	"time"
)

func TestCapTimeout(t *testing.T) {
	// A longer caller deadline is capped, so a hung Valkey cannot hold a cache call for 30s.
	long, cancelLong := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelLong()
	ctx, cancel := CapTimeout(long, 100*time.Millisecond)
	defer cancel()
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > 100*time.Millisecond {
		t.Fatalf("deadline under a 30s caller = %v, want within 100ms", time.Until(dl))
	}

	// An earlier caller deadline still wins.
	short, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	want, _ := short.Deadline()
	ctx2, cancel2 := CapTimeout(short, time.Second)
	defer cancel2()
	if dl, _ := ctx2.Deadline(); !dl.Equal(want) {
		t.Fatalf("deadline = %v, want the caller's earlier %v", dl, want)
	}

	// No deadline: d applies. d <= 0: ctx is returned as is.
	ctx3, cancel3 := CapTimeout(context.Background(), 50*time.Millisecond)
	defer cancel3()
	if _, ok := ctx3.Deadline(); !ok {
		t.Fatal("CapTimeout without a caller deadline should set one")
	}
	ctx4, cancel4 := CapTimeout(context.Background(), 0)
	defer cancel4()
	if _, ok := ctx4.Deadline(); ok {
		t.Fatal("CapTimeout with d = 0 should not set a deadline")
	}
}

func TestWithTimeoutKeepsCallerDeadline(t *testing.T) {
	long, cancelLong := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelLong()
	want, _ := long.Deadline()
	ctx, cancel := WithTimeout(long, 100*time.Millisecond)
	defer cancel()
	if dl, _ := ctx.Deadline(); !dl.Equal(want) {
		t.Fatalf("WithTimeout changed the caller deadline: %v, want %v", dl, want)
	}
}
