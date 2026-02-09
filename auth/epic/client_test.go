package epic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ceskypane/fngo/auth"
)

func TestDeviceAuthGrantReturnsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Fatalf("unexpected content-type: %s", got)
		}

		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}

		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "device_auth" {
			t.Fatalf("unexpected grant_type: %s", r.Form.Get("grant_type"))
		}

		_, _ = w.Write([]byte(`{"access_token":"a1","refresh_token":"r1","token_type":"bearer","expires_at":"2026-02-09T10:00:00Z","refresh_expires_at":"2026-03-01T10:00:00Z","account_id":"acc1"}`))
	}))
	defer srv.Close()

	client, err := NewClient(Config{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "sec", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	tokens, err := client.TokenByDeviceAuth(context.Background(), auth.DeviceAuth{AccountID: "acc1", DeviceID: "d1", Secret: "s1"})
	if err != nil {
		t.Fatalf("token by device auth: %v", err)
	}

	if tokens.AccessToken != "a1" || tokens.RefreshToken != "r1" {
		t.Fatalf("unexpected token set: %#v", tokens)
	}
}

func TestRefreshGrantReturnsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Fatalf("unexpected grant_type: %s", r.Form.Get("grant_type"))
		}

		_, _ = w.Write([]byte(`{"access_token":"a2","refresh_token":"r2","token_type":"bearer","expires_at":"2026-02-09T10:00:00Z"}`))
	}))
	defer srv.Close()

	client, err := NewClient(Config{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "sec", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	tokens, err := client.TokenByRefresh(context.Background(), "refresh1")
	if err != nil {
		t.Fatalf("token by refresh: %v", err)
	}

	if tokens.AccessToken != "a2" || tokens.RefreshToken != "r2" {
		t.Fatalf("unexpected token set: %#v", tokens)
	}
}

func TestInvalidGrantReturnsFatalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"bad refresh token"}`))
	}))
	defer srv.Close()

	client, err := NewClient(Config{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "sec", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.TokenByRefresh(context.Background(), "bad")
	if err == nil {
		t.Fatalf("expected error")
	}

	var oauthErr *OAuthError
	if !errors.As(err, &oauthErr) {
		t.Fatalf("expected OAuthError, got %T", err)
	}

	if !oauthErr.Fatal() {
		t.Fatalf("expected fatal invalid_grant")
	}
}

func TestTransient500RetriesBounded(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&calls, 1)
		if current < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errorMessage":"temporary"}`))
			return
		}

		_, _ = w.Write([]byte(`{"access_token":"a3","refresh_token":"r3","token_type":"bearer","expires_at":"2026-02-09T10:00:00Z"}`))
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "sec",
		HTTPClient:   srv.Client(),
		MaxRetries:   3,
		MinBackoff:   time.Millisecond,
		MaxBackoff:   time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.sleep = func(time.Duration) {}

	tokens, err := client.TokenByRefresh(context.Background(), "refresh")
	if err != nil {
		t.Fatalf("token by refresh: %v", err)
	}

	if tokens.AccessToken != "a3" {
		t.Fatalf("unexpected access token: %s", tokens.AccessToken)
	}

	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", atomic.LoadInt32(&calls))
	}
}
