package pubsub

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"
)

type receiveLoop struct {
	kind  string // "subscribe", "ssubscribe" or "psubscribe", as reported in confirmations
	names []string

	mu          sync.Mutex
	resubscribe map[string]bool // names dropped and not yet confirmed again
	confirmed   bool            // a confirmation arrived since the last drop
}

func (l *receiveLoop) build(b valkeygo.Builder) valkeygo.Completed {
	switch l.kind {
	case "ssubscribe":
		return b.Ssubscribe().Channel(l.names...).Build()
	case "psubscribe":
		return b.Psubscribe().Pattern(l.names...).Build()
	default:
		return b.Subscribe().Channel(l.names...).Build()
	}
}

func (l *receiveLoop) buildUnsubscribe(b valkeygo.Builder) valkeygo.Completed {
	switch l.kind {
	case "ssubscribe":
		return b.Sunsubscribe().Channel(l.names...).Build()
	case "psubscribe":
		return b.Punsubscribe().Pattern(l.names...).Build()
	default:
		return b.Unsubscribe().Channel(l.names...).Build()
	}
}

type subscription struct {
	owner   *subscriber
	names   []string
	handler Handler
	opts    subscribeOptions

	ctx       context.Context
	cancel    context.CancelFunc
	closing   atomic.Bool
	receivers []*receiveLoop
	loops     sync.WaitGroup
	dispatchW sync.WaitGroup
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once

	// events is an unbounded queue of lifecycle callbacks. emit runs on valkey-go's connection
	// goroutine, which must never block, so it only appends and signals.
	eventsMu sync.Mutex
	events   []func()
	signal   chan struct{}

	mu        sync.Mutex
	confirmed map[string]bool
	pending   int
	ready     chan struct{}
}

var _ ISubscription = (*subscription)(nil)

func newSubscription(owner *subscriber, names []string, handler Handler, o subscribeOptions) *subscription {
	ctx, cancel := context.WithCancel(context.Background())
	return &subscription{
		owner:     owner,
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
	}
}

func (s *subscription) Channels() []string { return append([]string(nil), s.names...) }

func (s *subscription) Done() <-chan struct{} { return s.done }

// Close unsubscribes, waits for the receive loops to stop and for a running callback to return.
// Callbacks still queued are dropped. Do not call Close from a handler or callback: it would wait
// for itself. Use `go sub.Close()` there instead.
func (s *subscription) Close() {
	s.once.Do(func() {
		// Cancelling Receive only detaches the local handler; the server keeps the subscription
		// until it is told otherwise, so unsubscribe explicitly first.
		s.closing.Store(true)
		client := s.owner.client
		for _, l := range s.receivers {
			ctx, cancel := context.WithTimeout(context.Background(), unsubscribeTimeout)
			_ = client.Do(ctx, l.buildUnsubscribe(client.B())).Error()
			cancel()
		}
		s.cancel()
		s.loops.Wait()
		close(s.stop)
		s.dispatchW.Wait()
		s.owner.remove(s)
		close(s.done)
	})
}

// run starts the dispatcher and the receive loops. The subscriber calls it under its lock, before
// the subscription can be seen by Close, so receivers and the WaitGroups are set before any Wait.
func (s *subscription) run(loops []*receiveLoop) {
	s.receivers = loops
	s.dispatchW.Add(1)
	go s.dispatch()
	for _, l := range loops {
		s.loops.Add(1)
		go s.receive(l)
	}
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
			fn()
		}
	}
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

func (s *subscription) receive(l *receiveLoop) {
	defer s.loops.Done()
	client := s.owner.client
	attempt := 0
	for {
		hookCtx := valkeygo.WithOnSubscriptionHook(s.ctx, func(ps valkeygo.PubSubSubscription) {
			if ps.Kind == l.kind {
				s.confirm(l, ps.Channel)
			}
		})
		err := client.Receive(hookCtx, l.build(client.B()), func(m valkeygo.PubSubMessage) {
			s.handler(Message{Channel: m.Channel, Pattern: m.Pattern, Payload: []byte(m.Message)})
		})
		if s.closing.Load() || s.ctx.Err() != nil || errors.Is(err, valkeygo.ErrClosing) {
			return
		}
		if err == nil {
			err = errUnsubscribed
		}

		l.mu.Lock()
		if l.confirmed {
			attempt = 0 // the last attempt worked for a while; restart the backoff
		}
		l.confirmed = false
		l.resubscribe = make(map[string]bool, len(l.names))
		for _, n := range l.names {
			l.resubscribe[n] = true
		}
		l.mu.Unlock()

		s.owner.logger.LogError("valkey: subscription to "+strings.Join(l.names, ", ")+" dropped, resubscribing", err)
		if fn := s.opts.onDisconnected; fn != nil {
			dropErr := err
			s.emit(func() { fn(dropErr) })
		}

		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff(attempt, s.opts.minBackoff, s.opts.maxBackoff)):
		}
		attempt++
	}
}

// confirm runs on the connection goroutine for every subscribe confirmation. It must not block.
func (s *subscription) confirm(l *receiveLoop, name string) {
	s.mu.Lock()
	first := !s.confirmed[name]
	if first {
		s.confirmed[name] = true
		s.pending--
		if s.pending == 0 {
			close(s.ready)
		}
	}
	s.mu.Unlock()

	l.mu.Lock()
	l.confirmed = true
	again := l.resubscribe[name]
	delete(l.resubscribe, name)
	l.mu.Unlock()

	if first && s.opts.onSubscribed != nil {
		fn := s.opts.onSubscribed
		s.emit(func() { fn(name) })
	}
	if again && s.opts.onResubscribed != nil {
		fn := s.opts.onResubscribed
		s.emit(func() { fn(name) })
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
