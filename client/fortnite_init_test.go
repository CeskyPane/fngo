package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	transporthttp "github.com/ceskypane/fngo/transport/http"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newClientWithRoundTripper(rt roundTripperFunc) *Client {
	httpClient := &http.Client{Transport: rt}
	return &Client{
		httpClient: transporthttp.NewClient(httpClient, nil, transporthttp.Config{}),
	}
}

func TestGrantAccess_IgnoresNotFound(t *testing.T) {
	called := 0
	c := newClientWithRoundTripper(func(r *http.Request) (*http.Response, error) {
		called++
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.String(), "/fortnite/api/game/v2/grant_access/acc") {
			t.Fatalf("unexpected url: %s", r.URL.String())
		}

		body := `{"errorCode":"errors.com.epicgames.common.not_found","errorMessage":"not found"}`
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	if err := c.grantAccess(context.Background(), "acc"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if called != 1 {
		t.Fatalf("expected 1 request, got %d", called)
	}
}

func TestGrantAccess_IgnoresAlreadyEntitled(t *testing.T) {
	c := newClientWithRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := `Client requested access grant but already has the requested access entitlement`
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	if err := c.grantAccess(context.Background(), "acc"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestGrantAccess_FailsOnOtherStatus(t *testing.T) {
	c := newClientWithRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := `{"errorCode":"errors.com.epicgames.common.server_error","errorMessage":"boom"}`
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	err := c.grantAccess(context.Background(), "acc")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "grant_access failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
