package friends

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	transporthttp "github.com/ceskypane/fngo/transport/http"
)

type testTokenProvider struct {
	token string
}

func (p testTokenProvider) AccessToken(_ context.Context) (string, error) {
	return p.token, nil
}

func (p testTokenProvider) Refresh(_ context.Context) error {
	return nil
}

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

func TestListFriendsUsesSummaryEndpointAndAuthHeader(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"friends":[{"accountId":"f1","displayName":"Friend One","created":"2026-02-11T12:00:00.000Z"}],"incoming":[],"outgoing":[],"blocklist":[]}`))
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), testTokenProvider{token: "token123"}, transporthttp.Config{MaxRetries: 0})
	cmd := NewCommands(httpClient, Config{BaseURL: srv.URL, AccountID: "self"})

	friendsList, err := cmd.ListFriends(context.Background())
	if err != nil {
		t.Fatalf("list friends: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("expected GET, got %s", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/friends/api/v1/self/summary") {
		t.Fatalf("unexpected path %s", gotPath)
	}
	if gotAuth != "Bearer token123" {
		t.Fatalf("unexpected auth header %q", gotAuth)
	}
	if len(friendsList) != 1 {
		t.Fatalf("expected 1 friend, got %d", len(friendsList))
	}
	if friendsList[0].AccountID != "f1" {
		t.Fatalf("unexpected account id %s", friendsList[0].AccountID)
	}
	if friendsList[0].DisplayName != "Friend One" {
		t.Fatalf("unexpected display name %s", friendsList[0].DisplayName)
	}
	if friendsList[0].CreatedAt == nil || !friendsList[0].CreatedAt.Equal(time.Date(2026, 2, 11, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected created at %#v", friendsList[0].CreatedAt)
	}
}

func TestListIncomingFriendRequestsParsesSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"friends":[],"incoming":[{"accountId":"in1","displayName":"Incoming One","created":"2026-02-10T11:00:00.000Z"}],"outgoing":[]}`))
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	cmd := NewCommands(httpClient, Config{BaseURL: srv.URL, AccountID: "self"})

	incoming, err := cmd.ListIncomingFriendRequests(context.Background())
	if err != nil {
		t.Fatalf("list incoming: %v", err)
	}
	if len(incoming) != 1 {
		t.Fatalf("expected 1 incoming request, got %d", len(incoming))
	}
	if incoming[0].AccountID != "in1" {
		t.Fatalf("unexpected incoming account id %s", incoming[0].AccountID)
	}
	if incoming[0].DisplayName != "Incoming One" {
		t.Fatalf("unexpected incoming display name %s", incoming[0].DisplayName)
	}
	if incoming[0].CreatedAt == nil {
		t.Fatalf("expected createdAt to be parsed")
	}
}

func TestListOutgoingFriendRequestsParsesSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"friends":[],"incoming":[],"outgoing":[{"accountId":"out1","displayName":"Outgoing One","created":"2026-02-10T12:30:00.000Z"}]}`))
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	cmd := NewCommands(httpClient, Config{BaseURL: srv.URL, AccountID: "self"})

	outgoing, err := cmd.ListOutgoingFriendRequests(context.Background())
	if err != nil {
		t.Fatalf("list outgoing: %v", err)
	}
	if len(outgoing) != 1 {
		t.Fatalf("expected 1 outgoing request, got %d", len(outgoing))
	}
	if outgoing[0].AccountID != "out1" {
		t.Fatalf("unexpected outgoing account id %s", outgoing[0].AccountID)
	}
	if outgoing[0].DisplayName != "Outgoing One" {
		t.Fatalf("unexpected outgoing display name %s", outgoing[0].DisplayName)
	}
	if outgoing[0].CreatedAt == nil {
		t.Fatalf("expected createdAt to be parsed")
	}
}

func TestCancelOutgoingFriendRequestUsesDeleteEndpoint(t *testing.T) {
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

	if err := cmd.CancelOutgoingFriendRequest(context.Background(), "target-out"); err != nil {
		t.Fatalf("cancel outgoing: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("expected DELETE, got %s", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/friends/api/v1/self/friends/target-out") {
		t.Fatalf("unexpected path %s", gotPath)
	}
}

func TestDeclineIncomingFriendRequestUsesDeleteEndpoint(t *testing.T) {
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

	if err := cmd.DeclineIncomingFriendRequest(context.Background(), "target-in"); err != nil {
		t.Fatalf("decline incoming: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("expected DELETE, got %s", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/friends/api/v1/self/friends/target-in") {
		t.Fatalf("unexpected path %s", gotPath)
	}
}

func TestRemoveAllFriendsUsesListThenDelete(t *testing.T) {
	var deletePaths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/friends/api/v1/self/summary"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"friends":[{"accountId":"f1"},{"accountId":"f2"}],"incoming":[],"outgoing":[]}`))
			return
		case r.Method == http.MethodDelete:
			deletePaths = append(deletePaths, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	cmd := NewCommands(httpClient, Config{BaseURL: srv.URL, AccountID: "self"})

	result, err := cmd.RemoveAllFriends(context.Background())
	if err != nil {
		t.Fatalf("remove all friends: %v", err)
	}
	if result.Attempted != 2 {
		t.Fatalf("expected attempted=2, got %d", result.Attempted)
	}
	if result.Removed != 2 {
		t.Fatalf("expected removed=2, got %d", result.Removed)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("expected failed=0, got %d", len(result.Failed))
	}
	if len(deletePaths) != 2 {
		t.Fatalf("expected 2 delete calls, got %d", len(deletePaths))
	}
}
