package transporthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type fakeTokenProvider struct {
	mu           sync.Mutex
	token        string
	refreshToken string
	refreshCalls int
	refreshErr   error
}

func (f *fakeTokenProvider) AccessToken(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.token, nil
}

func (f *fakeTokenProvider) Refresh(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.refreshCalls++
	if f.refreshErr != nil {
		return f.refreshErr
	}

	f.token = f.refreshToken
	return nil
}

func (f *fakeTokenProvider) RefreshCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.refreshCalls
}

func TestRequestRetriesOn500ThenSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), nil, Config{MaxRetries: 2, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	c.sleep = func(time.Duration) {}

	resp, err := c.Request(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestRequestRetriesOn429RetryAfterThenSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), nil, Config{MaxRetries: 2, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	slept := make([]time.Duration, 0, 2)
	c.sleep = func(d time.Duration) {
		slept = append(slept, d)
	}

	resp, err := c.Request(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	if len(slept) == 0 || slept[0] != time.Second {
		t.Fatalf("expected retry-after sleep of 1s, got %v", slept)
	}
}

func TestRequestRefreshesOn401EpicAuthThenSuccess(t *testing.T) {
	provider := &fakeTokenProvider{token: "old", refreshToken: "new"}
	var calls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "Bearer old" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.common.oauth.invalid_token"}`))
			return
		}

		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.common.oauth.invalid_token"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), provider, Config{MaxRetries: 1, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	c.sleep = func(time.Duration) {}

	resp, err := c.Request(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	if provider.RefreshCalls() != 1 {
		t.Fatalf("expected one refresh call, got %d", provider.RefreshCalls())
	}

	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestRequest401NonAuthDoesNotRefresh(t *testing.T) {
	provider := &fakeTokenProvider{token: "old", refreshToken: "new"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.social.party.party_query_forbidden"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), provider, Config{MaxRetries: 1, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	c.sleep = func(time.Duration) {}

	_, err := c.Request(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	if err == nil {
		t.Fatalf("expected unauthorized error")
	}

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}

	if provider.RefreshCalls() != 0 {
		t.Fatalf("expected no refresh calls, got %d", provider.RefreshCalls())
	}
}

func TestRequest401RefreshFails(t *testing.T) {
	provider := &fakeTokenProvider{token: "old", refreshErr: errors.New("no refresh")}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.common.oauth.invalid_token"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), provider, Config{MaxRetries: 1, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	c.sleep = func(time.Duration) {}

	_, err := c.Request(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	if err == nil {
		t.Fatalf("expected error")
	}

	if !errors.Is(err, ErrRefreshFailed) {
		t.Fatalf("expected refresh failed error, got %v", err)
	}

	if provider.RefreshCalls() != 1 {
		t.Fatalf("expected one refresh call, got %d", provider.RefreshCalls())
	}
}

func TestRequestCorrelationIDHeaderInjection(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Correlation-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), nil, Config{
		MaxRetries:          0,
		MinBackoff:          time.Millisecond,
		MaxBackoff:          time.Millisecond,
		CorrelationIDHeader: "X-Correlation-Id",
		CorrelationID:       func() string { return "cid-123" },
	})

	_, err := c.Request(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if got != "cid-123" {
		t.Fatalf("unexpected correlation id: %s", got)
	}
}

func TestRequest5xxRetryLimit(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), nil, Config{MaxRetries: 2, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	c.sleep = func(time.Duration) {}

	_, err := c.Request(context.Background(), Request{Method: http.MethodGet, URL: srv.URL})
	if err == nil {
		t.Fatalf("expected error")
	}

	if calls != 3 {
		t.Fatalf("expected 3 calls (1 + 2 retries), got %d", calls)
	}
}
