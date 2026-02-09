package events

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWaitForCompletesOnMatch(t *testing.T) {
	bus := NewBus()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan Event, 1)
	go func() {
		evt, err := bus.WaitFor(ctx, func(e Event) bool {
			return e.Name() == EventClientReady
		})
		if err != nil {
			done <- nil
			return
		}

		done <- evt
	}()

	waitForWaiterCount(t, bus, 1)

	_ = bus.Emit(ClientReady{Base: Base{At: time.Now().UTC()}})

	select {
	case evt := <-done:
		if evt == nil {
			t.Fatalf("expected matching event")
		}

		if evt.Name() != EventClientReady {
			t.Fatalf("unexpected event: %s", evt.Name())
		}
	case <-time.After(time.Second):
		t.Fatalf("waiter did not complete")
	}
}

func TestWaitForTimeout(t *testing.T) {
	bus := NewBus()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := bus.WaitFor(ctx, func(e Event) bool {
		return false
	})
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestWaitForCancel(t *testing.T) {
	bus := NewBus()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := bus.WaitFor(ctx, func(e Event) bool {
		return true
	})
	if !errors.Is(err, ErrWaitCanceled) {
		t.Fatalf("expected cancel, got %v", err)
	}
}

func TestMultipleWaitersNoDeadlock(t *testing.T) {
	bus := NewBus()

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)

	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			evt, err := bus.WaitFor(ctx, func(e Event) bool {
				return e.Name() == EventXMPPConnected
			})
			if err != nil {
				errs <- err
				return
			}

			if evt.Name() != EventXMPPConnected {
				errs <- errors.New("unexpected event type")
				return
			}
		}()
	}

	waitForWaiterCount(t, bus, n)

	_ = bus.Emit(XMPPConnected{Base: Base{At: time.Now().UTC()}, Endpoint: "wss://example"})

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("waiter error: %v", err)
		}
	}
}

func waitForWaiterCount(t *testing.T, bus *Bus, target int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for {
		bus.mu.Lock()
		n := len(bus.waiters)
		bus.mu.Unlock()

		if n >= target {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("waiters did not register: have=%d target=%d", n, target)
		}

		time.Sleep(5 * time.Millisecond)
	}
}
