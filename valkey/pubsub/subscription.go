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
	events    chan func()
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once

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
		events:    make(chan func(), eventBuffer),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		confirmed: make(map[string]bool, len(names)),
		pending:   len(names),
		ready:     make(chan struct{}),
	}
}

func (s *subscription) Channels() []string { return append([]string(nil), s.names...) }

func (s *subscription) Done() <-chan struct{} { return s.done }

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
		s.owner.remove(s)
		close(s.done)
	})
}

func (s *subscription) run(loops []*receiveLoop) {
	s.receivers = loops
	go s.dispatch()
	for _, l := range loops {
		s.loops.Add(1)
		go s.receive(l)
	}
}

// dispatch runs lifecycle callbacks one at a time, in order, off the connection goroutine.
func (s *subscription) dispatch() {
	for {
		select {
		case fn := <-s.events:
			fn()
		case <-s.stop:
			return
		}
	}
}

func (s *subscription) emit(fn func()) {
	select {
	case s.events <- fn:
	case <-s.ctx.Done():
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

// confirm runs on the connection goroutine for every subscribe confirmation.
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
