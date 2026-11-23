package testutil

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// StallProxy forwards TCP connections to a target until Stall is called. After that it reads and
// drops everything it receives, so the server looks hung: connections stay open and no reply ever
// comes, which miniredis alone cannot do.
type StallProxy struct {
	ln      net.Listener
	target  string
	stalled atomic.Bool

	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

// NewStallProxy listens on a local port and forwards to target. It is closed at test end.
func NewStallProxy(t *testing.T, target string) *StallProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("stall proxy listen: %v", err)
	}
	p := &StallProxy{ln: ln, target: target}
	go p.accept()
	t.Cleanup(p.close)
	return p
}

// Addr is the address clients should connect to.
func (p *StallProxy) Addr() string { return p.ln.Addr().String() }

// Stall stops forwarding in both directions on every connection, current and future.
func (p *StallProxy) Stall() { p.stalled.Store(true) }

func (p *StallProxy) accept() {
	for {
		in, err := p.ln.Accept()
		if err != nil {
			return
		}
		out, err := net.Dial("tcp", p.target)
		if err != nil {
			in.Close()
			continue
		}
		if !p.track(in, out) {
			return
		}
		go p.forward(out, in)
		go p.forward(in, out)
	}
}

func (p *StallProxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		for _, c := range conns {
			c.Close()
		}
		return false
	}
	p.conns = append(p.conns, conns...)
	return true
}

func (p *StallProxy) forward(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if err != nil {
			dst.Close()
			return
		}
		if p.stalled.Load() {
			continue
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			src.Close()
			return
		}
	}
}

func (p *StallProxy) close() {
	p.mu.Lock()
	p.closed = true
	conns := p.conns
	p.conns = nil
	p.mu.Unlock()
	p.ln.Close()
	for _, c := range conns {
		c.Close()
	}
}
