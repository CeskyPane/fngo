package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/internal/backoff"
	"github.com/ceskypane/fngo/logging"
)

type Refresher interface {
	Refresh(ctx context.Context, current TokenSet) (TokenSet, error)
}

type SchedulerConfig struct {
	TokenStore             TokenStore
	Refresher              Refresher
	Bus                    *events.Bus
	RefreshSkew            time.Duration
	MinRefreshInterval     time.Duration
	RefreshRetryMinBackoff time.Duration
	RefreshRetryMaxBackoff time.Duration
	Now                    func() time.Time
	After                  func(d time.Duration) <-chan time.Time
	Logger                 logging.Logger
}

type RefreshScheduler struct {
	cfg SchedulerConfig
}

func NewRefreshScheduler(cfg SchedulerConfig) (*RefreshScheduler, error) {
	if cfg.TokenStore == nil {
		return nil, errors.New("auth: token store is required")
	}

	if cfg.Refresher == nil {
		return nil, errors.New("auth: refresher is required")
	}

	if cfg.Bus == nil {
		cfg.Bus = events.NewBus()
	}

	if cfg.RefreshSkew <= 0 {
		cfg.RefreshSkew = 30 * time.Second
	}

	if cfg.MinRefreshInterval <= 0 {
		cfg.MinRefreshInterval = time.Second
	}

	if cfg.RefreshRetryMinBackoff <= 0 {
		cfg.RefreshRetryMinBackoff = 250 * time.Millisecond
	}

	if cfg.RefreshRetryMaxBackoff <= 0 {
		cfg.RefreshRetryMaxBackoff = 5 * time.Second
	}

	if cfg.RefreshRetryMaxBackoff < cfg.RefreshRetryMinBackoff {
		cfg.RefreshRetryMaxBackoff = cfg.RefreshRetryMinBackoff
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if cfg.After == nil {
		cfg.After = time.After
	}

	cfg.Logger = logging.With(cfg.Logger)

	return &RefreshScheduler{cfg: cfg}, nil
}

func (s *RefreshScheduler) Run(ctx context.Context) {
	refreshFailures := 0

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		tokens, ok, err := s.cfg.TokenStore.Load(ctx)
		if err != nil {
			s.cfg.Logger.Warn("auth refresh scheduler: token load failed", logging.F("error", err.Error()))
			s.emit(events.AuthRefreshFailed{Base: events.Base{At: s.cfg.Now().UTC()}, Err: fmt.Errorf("load token set: %w", err)})
			if !s.wait(ctx, s.cfg.MinRefreshInterval) {
				return
			}

			continue
		}

		if !ok {
			if !s.wait(ctx, s.cfg.MinRefreshInterval) {
				return
			}

			continue
		}

		refreshAt := tokens.ExpiresAt.Add(-s.cfg.RefreshSkew)
		delay := refreshAt.Sub(s.cfg.Now())
		if delay < s.cfg.MinRefreshInterval {
			delay = s.cfg.MinRefreshInterval
		}

		if !s.wait(ctx, delay) {
			return
		}

		current, currentOK, loadErr := s.cfg.TokenStore.Load(ctx)
		if loadErr != nil {
			s.emit(events.AuthRefreshFailed{Base: events.Base{At: s.cfg.Now().UTC()}, Err: fmt.Errorf("reload token set: %w", loadErr)})
			continue
		}

		if !currentOK {
			continue
		}

		updated, refreshErr := s.cfg.Refresher.Refresh(ctx, current)
		if refreshErr != nil {
			if IsFatal(refreshErr) {
				s.cfg.Logger.Error("auth refresh scheduler: fatal refresh failure", logging.F("error", refreshErr.Error()))
				s.emit(events.AuthFatal{Base: events.Base{At: s.cfg.Now().UTC()}, Err: refreshErr})
				return
			}

			s.cfg.Logger.Warn("auth refresh scheduler: transient refresh failure", logging.F("error", refreshErr.Error()))
			s.emit(events.AuthRefreshFailed{Base: events.Base{At: s.cfg.Now().UTC()}, Err: refreshErr})

			delay = backoff.Exponential(refreshFailures, s.cfg.RefreshRetryMinBackoff, s.cfg.RefreshRetryMaxBackoff)
			refreshFailures++

			if !s.wait(ctx, delay) {
				return
			}

			continue
		}

		if saveErr := s.cfg.TokenStore.Save(ctx, updated); saveErr != nil {
			s.cfg.Logger.Warn("auth refresh scheduler: token save failed", logging.F("error", saveErr.Error()))
			s.emit(events.AuthRefreshFailed{Base: events.Base{At: s.cfg.Now().UTC()}, Err: fmt.Errorf("save token set: %w", saveErr)})
			continue
		}

		refreshFailures = 0
		s.cfg.Logger.Info("auth refresh scheduler: refresh succeeded", logging.F("expires_at", updated.ExpiresAt.UTC().Format(time.RFC3339)))
		s.emit(events.AuthRefreshed{Base: events.Base{At: s.cfg.Now().UTC()}, ExpiresAt: updated.ExpiresAt})
	}
}

func (s *RefreshScheduler) wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = s.cfg.MinRefreshInterval
	}

	select {
	case <-ctx.Done():
		return false
	case <-s.cfg.After(d):
		return true
	}
}

func (s *RefreshScheduler) emit(evt events.Event) {
	_ = s.cfg.Bus.Emit(evt)
}
