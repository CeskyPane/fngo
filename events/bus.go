package events

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrBusClosed    = errors.New("events: bus closed")
	ErrWaitTimeout  = errors.New("events: waiter timed out")
	ErrWaitCanceled = errors.New("events: waiter canceled")
)

type Predicate func(Event) bool

type waiter struct {
	pred Predicate
	ch   chan Event
}

type Subscription struct {
	C      <-chan Event
	cancel func()
	once   sync.Once
}

func (s *Subscription) Cancel() {
	if s == nil {
		return
	}

	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}

type Bus struct {
	mu      sync.Mutex
	closed  bool
	nextID  uint64
	subs    map[uint64]chan Event
	waiters map[uint64]*waiter
}

func NewBus() *Bus {
	return &Bus{
		subs:    make(map[uint64]chan Event),
		waiters: make(map[uint64]*waiter),
	}
}

func (b *Bus) Subscribe(buffer int) (*Subscription, error) {
	if buffer <= 0 {
		buffer = 1
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, ErrBusClosed
	}

	id := b.nextID
	b.nextID++

	ch := make(chan Event, buffer)
	b.subs[id] = ch

	sub := &Subscription{}
	sub.C = ch
	sub.cancel = func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		subCh, ok := b.subs[id]
		if !ok {
			return
		}

		delete(b.subs, id)
		close(subCh)
	}

	return sub, nil
}

func (b *Bus) Emit(evt Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrBusClosed
	}

	for _, ch := range b.subs {
		select {
		case ch <- evt:
		default:
		}
	}

	for id, w := range b.waiters {
		if w.pred != nil && !w.pred(evt) {
			continue
		}

		delete(b.waiters, id)
		w.ch <- evt
		close(w.ch)
	}

	return nil
}

func (b *Bus) WaitFor(ctx context.Context, pred Predicate) (Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	w := &waiter{
		pred: pred,
		ch:   make(chan Event, 1),
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrBusClosed
	}

	id := b.nextID
	b.nextID++
	b.waiters[id] = w
	b.mu.Unlock()

	select {
	case evt, ok := <-w.ch:
		if ok {
			return evt, nil
		}

		if b.IsClosed() {
			return nil, ErrBusClosed
		}

		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, ErrWaitTimeout
			}

			return nil, ErrWaitCanceled
		}

		return nil, ErrWaitCanceled
	case <-ctx.Done():
		b.mu.Lock()
		stored, ok := b.waiters[id]
		if ok {
			delete(b.waiters, id)
			close(stored.ch)
		}
		b.mu.Unlock()

		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrWaitTimeout
		}

		return nil, ErrWaitCanceled
	}
}

func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	b.closed = true

	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}

	for id, w := range b.waiters {
		delete(b.waiters, id)
		close(w.ch)
	}
}

func (b *Bus) IsClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.closed
}
