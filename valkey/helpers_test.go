package valkey

import (
	"net"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/carwale/golibraries/gologger"
	"github.com/carwale/golibraries/valkey/internal/testutil"
)

func discardLogger() gologger.ILogger { return testutil.DiscardLogger() }

func localConfig(t *testing.T, addr string) Config {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	return Config{Host: host, Port: port, IsCluster: Bool(false), TLS: Bool(false), AuthMode: AuthNone}
}

// newTestClient starts miniredis and connects a Client to it without auth or TLS.
func newTestClient(t *testing.T, opts ...Option) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := NewClient(localConfig(t, mr.Addr()), append([]Option{WithLogger(discardLogger())}, opts...)...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(c.Close)
	return c, mr
}
