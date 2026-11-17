// Package testutil starts miniredis and connects valkey-go clients to it, for package tests.
package testutil

import (
	"io"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/carwale/golibraries/gologger"
	valkeygo "github.com/valkey-io/valkey-go"
)

// DiscardLogger returns a logger that writes nothing.
func DiscardLogger() gologger.ILogger {
	return gologger.NewLoggerFactory().CreateZerologLogger(gologger.WithOutput(io.Discard))
}

// Option returns valkey-go options for a plain connection to addr.
func Option(addr string) valkeygo.ClientOption {
	return valkeygo.ClientOption{InitAddress: []string{addr}, ForceSingleClient: true, DisableCache: true}
}

// NewClient starts miniredis and connects a valkey-go client to it. Both are closed at test end.
func NewClient(t *testing.T) (valkeygo.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return Connect(t, Option(mr.Addr())), mr
}

// Connect opens a client with the given options and closes it at test end.
func Connect(t *testing.T, opt valkeygo.ClientOption) valkeygo.Client {
	t.Helper()
	c, err := valkeygo.NewClient(opt)
	if err != nil {
		t.Fatalf("valkey-go NewClient: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}
