package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ceskypane/fngo/events"
)

type fakeRefresher struct {
	mu     sync.Mutex
	calls  int
	result TokenSet
	err    error
}

func (f *fakeRefresher) Refresh(_ context.Context, _ TokenSet) (TokenSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.err != nil {
		return TokenSet{}, f.err
	}

	return f.result.Clone(), nil
}

func (f *fakeRefresher) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls
}

type fatalErr struct{}

func (fatalErr) Error() string {
	return "fatal"
}

func (fatalErr) Fatal() bool {
	return true
}

func TestRefreshSchedulerEmitsRefreshed(t *testing.T) {
	store := NewMemoryTokenStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Save(context.Background(), TokenSet{
		AccessToken:  "old",
		RefreshToken: "r1",
		ExpiresAt:    now.Add(time.Minute),
	})

	bus := NewTestBus(t)
	after := make(chan time.Time, 2)
	ref := &fakeRefresher{result: TokenSet{AccessToken: "new", RefreshToken: "r2", ExpiresAt: now.Add(2 * time.Minute)}}

	s, err := NewRefreshScheduler(SchedulerConfig{
		TokenStore:             store,
		Refresher:              ref,
		Bus:                    bus,
		RefreshSkew:            30 * time.Second,
		MinRefreshInterval:     10 * time.Millisecond,
		RefreshRetryMinBackoff: 10 * time.Millisecond,
		RefreshRetryMaxBackoff: 20 * time.Millisecond,
		Now:                    func() time.Time { return now },
		After:                  func(_ time.Duration) <-chan time.Time { return after },
	})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}

	sub, err := bus.Subscribe(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	after <- now

	select {
	case evt := <-sub.C:
		if evt.Name() != events.EventAuthRefreshed {
			t.Fatalf("unexpected event %s", evt.Name())
		}
	case <-time.After(time.Second):
		t.Fatalf("expected auth refreshed event")
	}

	got, ok, loadErr := store.Load(context.Background())
	if loadErr != nil {
		t.Fatalf("load: %v", loadErr)
	}

	if !ok || got.AccessToken != "new" {
		t.Fatalf("token set not updated: %#v ok=%v", got, ok)
	}

	if ref.Calls() == 0 {
		t.Fatalf("expected refresh call")
	}
}

func TestRefreshSchedulerEmitsFailure(t *testing.T) {
	store := NewMemoryTokenStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Save(context.Background(), TokenSet{
		AccessToken:  "old",
		RefreshToken: "r1",
		ExpiresAt:    now.Add(time.Minute),
	})

	bus := NewTestBus(t)
	after := make(chan time.Time, 4)
	ref := &fakeRefresher{err: errors.New("refresh failed")}

	s, err := NewRefreshScheduler(SchedulerConfig{
		TokenStore:             store,
		Refresher:              ref,
		Bus:                    bus,
		RefreshSkew:            30 * time.Second,
		MinRefreshInterval:     10 * time.Millisecond,
		RefreshRetryMinBackoff: 10 * time.Millisecond,
		RefreshRetryMaxBackoff: 20 * time.Millisecond,
		Now:                    func() time.Time { return now },
		After:                  func(_ time.Duration) <-chan time.Time { return after },
	})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}

	sub, err := bus.Subscribe(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	after <- now
	after <- now

	select {
	case evt := <-sub.C:
		if evt.Name() != events.EventAuthRefreshFailed {
			t.Fatalf("unexpected event %s", evt.Name())
		}
	case <-time.After(time.Second):
		t.Fatalf("expected auth refresh failure event")
	}
}

func TestRefreshSchedulerStopsOnFatal(t *testing.T) {
	store := NewMemoryTokenStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Save(context.Background(), TokenSet{
		AccessToken:  "old",
		RefreshToken: "r1",
		ExpiresAt:    now.Add(time.Minute),
	})

	bus := NewTestBus(t)
	after := make(chan time.Time, 4)
	ref := &fakeRefresher{err: fatalErr{}}

	s, err := NewRefreshScheduler(SchedulerConfig{
		TokenStore:             store,
		Refresher:              ref,
		Bus:                    bus,
		RefreshSkew:            30 * time.Second,
		MinRefreshInterval:     10 * time.Millisecond,
		RefreshRetryMinBackoff: 10 * time.Millisecond,
		RefreshRetryMaxBackoff: 20 * time.Millisecond,
		Now:                    func() time.Time { return now },
		After:                  func(_ time.Duration) <-chan time.Time { return after },
	})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}

	sub, err := bus.Subscribe(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	after <- now

	select {
	case evt := <-sub.C:
		if evt.Name() != events.EventAuthFatal {
			t.Fatalf("unexpected event %s", evt.Name())
		}
	case <-time.After(time.Second):
		t.Fatalf("expected auth fatal event")
	}

	calls := ref.Calls()
	after <- now
	after <- now
	time.Sleep(20 * time.Millisecond)
	if ref.Calls() != calls {
		t.Fatalf("expected scheduler to stop after fatal refresh")
	}
}

func NewTestBus(t *testing.T) *events.Bus {
	t.Helper()

	return events.NewBus()
}
