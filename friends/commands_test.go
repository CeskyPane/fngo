package friends

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	transporthttp "github.com/ceskypane/fngo/transport/http"
)

func TestAddFriendCallsExpectedEndpoint(t *testing.T) {
	var gotMethod string
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	cmd := NewCommands(httpClient, Config{BaseURL: srv.URL, AccountID: "self"})

	if err := cmd.AddFriend(context.Background(), "target"); err != nil {
		t.Fatalf("add friend: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}

	if !strings.HasSuffix(gotPath, "/friends/api/public/friends/self/target") {
		t.Fatalf("unexpected path %s", gotPath)
	}
}

func TestRemoveFriendCallsExpectedEndpoint(t *testing.T) {
	var gotMethod string
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	cmd := NewCommands(httpClient, Config{BaseURL: srv.URL, AccountID: "self"})

	if err := cmd.RemoveFriend(context.Background(), "target"); err != nil {
		t.Fatalf("remove friend: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Fatalf("expected DELETE, got %s", gotMethod)
	}

	if !strings.HasSuffix(gotPath, "/friends/api/v1/self/friends/target") {
		t.Fatalf("unexpected path %s", gotPath)
	}
}

func TestAddFriendDecodesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.friends.incoming_friendships_disabled","errorMessage":"blocked"}`))
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	cmd := NewCommands(httpClient, Config{BaseURL: srv.URL, AccountID: "self"})

	err := cmd.AddFriend(context.Background(), "target")
	if err == nil {
		t.Fatalf("expected error")
	}

	apiErr := &APIError{}
	if ok := errors.As(err, &apiErr); !ok {
		t.Fatalf("expected APIError, got %T", err)
	}

	if apiErr.Code != "errors.com.epicgames.friends.incoming_friendships_disabled" {
		t.Fatalf("unexpected error code %s", apiErr.Code)
	}
}
