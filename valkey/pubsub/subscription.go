package pubsub

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"
)

// buildUnsubscribe builds the unsubscribe command matching a subscribe kind.
func buildUnsubscribe(b valkeygo.Builder, kind string, names []string) valkeygo.Completed {
	switch kind {
	case "ssubscribe":
		return b.Sunsubscribe().Channel(names...).Build()
	case "psubscribe":
		return b.Punsubscribe().Pattern(names...).Build()
	default:
		return b.Unsubscribe().Channel(names...).Build()
	}
}

type subscription struct {
	owner   *subscriber
	kind    string // "subscribe", "ssubscribe" or "psubscribe"
	names   []string
	handler Handler
	opts    subscribeOptions

	// ctx is cancelled by Close; emit drops callbacks after that.
	ctx       context.Context
	cancel    context.CancelFunc
	feeds     []*feed // set by the subscriber under refMu
	dispatchW sync.WaitGroup
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once

	// deliverMu lets Close wait for a handler call in progress; detached is set under its write
	// lock, after which no handler call starts.
	deliverMu sync.RWMutex
	detached  bool

	// events is an unbounded queue of lifecycle callbacks. emit runs on valkey-go's connection
	// goroutine, which must never block, so it only appends and signals.
	eventsMu sync.Mutex
	events   []func()
	signal   chan struct{}

	mu        sync.Mutex
	confirmed map[string]bool // names confirmed at least once
	pending   int             // names never confirmed yet
	ready     chan struct{}
	up        map[string]bool // names confirmed since their last drop
	upCount   int
	outage    bool  // OnDisconnected was reported and not every name is back yet
	lastErr   error // latest receive error; Subscribe reports it when confirmation times out

	// failed is closed when the server rejects the subscription before it was ever confirmed (for
	// example NOPERM): retrying cannot help, so Subscribe returns the error at once.
	failed     chan struct{}
	failedOnce sync.Once
}

var _ ISubscription = (*subscription)(nil)

func newSubscription(owner *subscriber, kind string, names []string, handler Handler, o subscribeOptions) *subscription {
	ctx, cancel := context.WithCancel(context.Background())
	return &subscription{
		owner:     owner,
		kind:      kind,
		names:     names,
		handler:   handler,
		opts:      o,
		ctx:       ctx,
		cancel:    cancel,
		signal:    make(chan struct{}, 1),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		confirmed: make(map[string]bool, len(names)),
		pending:   len(names),
		ready:     make(chan struct{}),
		up:        make(map[string]bool, len(names)),
		failed:    make(chan struct{}),
	}
}

func (s *subscription) Channels() []string { return append([]string(nil), s.names...) }

func (s *subscription) Done() <-chan struct{} { return s.done }

// Close detaches from the shared feeds (unsubscribing the names no other subscription uses), waits
// for a running handler and callback to return, and drops callbacks still queued. Do not call
// Close from a handler or callback: it would wait for itself. Use `go sub.Close()` there instead.
func (s *subscription) Close() {
	s.once.Do(func() {
		s.cancel()
		s.owner.release(s)
		s.deliverMu.Lock()
		s.detached = true
		s.deliverMu.Unlock()
		close(s.stop)
		s.dispatchW.Wait()
		s.owner.remove(s)
		close(s.done)
	})
}

// dispatch runs lifecycle callbacks one at a time, in order, on its own goroutine, so a slow
// callback never holds up message delivery.
func (s *subscription) dispatch() {
	defer s.dispatchW.Done()
	for {
		select {
		case <-s.signal:
		case <-s.stop:
			return
		}
		for {
			s.eventsMu.Lock()
			if len(s.events) == 0 {
				s.events = nil // release the backing array after a burst
				s.eventsMu.Unlock()
				break
			}
			fn := s.events[0]
			s.events[0] = nil
			s.events = s.events[1:]
			s.eventsMu.Unlock()

			select {
			case <-s.stop:
				return
			default:
			}
			s.runCallback(fn)
		}
	}
}

// runCallback runs one lifecycle callback. A panic is recovered and logged, so a bad callback
// cannot kill the process while Valkey is flapping; the queue carries on with the next one.
func (s *subscription) runCallback(fn func()) {
	defer func() {
		if p := recover(); p != nil {
			s.owner.logger.LogError("valkey: pub/sub lifecycle callback panicked for "+strings.Join(s.names, ", "), fmt.Errorf("panic: %v", p))
		}
	}()
	fn()
}

// emit queues a callback. It never blocks: it runs on valkey-go's connection goroutine (through the
// subscription hook), and blocking there would stop every message and command on the connection.
func (s *subscription) emit(fn func()) {
	if s.ctx.Err() != nil {
		return
	}
	s.eventsMu.Lock()
	s.events = append(s.events, fn)
	s.eventsMu.Unlock()
	select {
	case s.signal <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// deliver runs the handler for one message. A panic is recovered and logged, so a bad message
// cannot kill the process; that message is dropped.
func (s *subscription) deliver(m valkeygo.PubSubMessage) {
	s.deliverMu.RLock()
	defer s.deliverMu.RUnlock()
	if s.detached {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			s.owner.logger.LogError("valkey: pub/sub handler panicked on channel "+m.Channel, fmt.Errorf("panic: %v", p))
		}
	}()
	s.handler(Message{Channel: m.Channel, Pattern: m.Pattern, Payload: []byte(m.Message)})
}

func (s *subscription) isReady() bool {
	select {
	case <-s.ready:
		return true
	default:
		return false
	}
}

// rejected reports whether err is the server refusing this client the command, which a retry
// cannot fix: NOPERM (ACL) and NOAUTH / WRONGPASS (credentials). Everything else is retried until
// the confirmation timeout: network errors, transient replies (LOADING, TRYAGAIN, CLUSTERDOWN,
// MOVED) and generic ERR replies, some of which are temporary ("max number of clients reached").
func rejected(err error) bool {
	verr, ok := valkeygo.IsValkeyErr(err)
	if !ok {
		return false
	}
	msg := verr.Error()
	for _, prefix := range []string{"NOPERM", "NOAUTH", "WRONGPASS"} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

func (s *subscription) lastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// confirm records that the server confirmed name. It runs on the connection goroutine (or under the
// feed lock when attaching to a confirmed name) and must not block.
func (s *subscription) confirm(name string) {
	s.mu.Lock()
	first := !s.confirmed[name]
	if first {
		s.confirmed[name] = true
		s.pending--
		if s.pending == 0 {
			close(s.ready)
		}
	}
	again := !first && !s.up[name]
	if !s.up[name] {
		s.up[name] = true
		s.upCount++
	}
	if s.upCount == len(s.names) {
		s.outage = false
	}
	s.mu.Unlock()

	if first && s.opts.onSubscribed != nil {
		fn := s.opts.onSubscribed
		s.emit(func() { fn(name) })
	}
	if again && s.opts.onResubscribed != nil {
		fn := s.opts.onResubscribed
		s.emit(func() { fn(name) })
	}
}

// dropped records a failed or ended receive for name. OnDisconnected is reported once per outage:
// on the first confirmed name to drop, and not again until every name has been confirmed again.
// Failed attempts while starting or reconnecting are not new disconnections.
func (s *subscription) dropped(name string, err error) {
	s.mu.Lock()
	s.lastErr = err
	report := false
	if s.up[name] {
		s.up[name] = false
		s.upCount--
		if !s.outage {
			s.outage = true
			report = true
		}
	}
	s.mu.Unlock()

	if !s.isReady() && rejected(err) {
		// The server refused the subscription itself (ACL, bad name): fail Subscribe now instead of
		// retrying until it times out.
		s.failedOnce.Do(func() { close(s.failed) })
	}
	if fn := s.opts.onDisconnected; fn != nil && report {
		s.emit(func() { fn(err) })
	}
}

// backoff returns an exponential delay between min and max with jitter in its upper half.
func backoff(attempt int, min, max time.Duration) time.Duration {
	d := min
	for i := 0; i < attempt && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + time.Duration(rand.Int63n(int64(half)+1)) //nolint:gosec // jitter, not security
}
