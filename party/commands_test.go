package party

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	transporthttp "github.com/ceskypane/fngo/transport/http"
)

func TestPatchPartyMetaRetriesOnStaleRevision(t *testing.T) {
	var mu sync.Mutex
	patchCalls := 0
	patchRevisions := make([]int64, 0, 2)
	partyFetches := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/user/acc1") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[` + partyJSONWith(10, 5, "", "NotReady") + `]}`))
			return
		}

		if strings.HasSuffix(path, "/parties/p1") && r.Method == http.MethodGet {
			mu.Lock()
			partyFetches++
			fetch := partyFetches
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			if fetch == 1 {
				_, _ = w.Write([]byte(partyJSONWith(11, 5, "", "NotReady")))
				return
			}

			_, _ = w.Write([]byte(partyJSONWith(12, 5, "new-key", "NotReady")))
			return
		}

		if strings.HasSuffix(path, "/parties/p1") && r.Method == http.MethodPatch {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)

			rev := int64(0)
			if rv, ok := payload["revision"].(float64); ok {
				rev = int64(rv)
			}

			mu.Lock()
			patchCalls++
			patchRevisions = append(patchRevisions, rev)
			callNo := patchCalls
			mu.Unlock()

			if callNo == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.social.party.stale_revision","errorMessage":"stale","messageVars":["10","11"]}`))
				return
			}

			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{
		MaxRetries: 0,
		MinBackoff: time.Millisecond,
		MaxBackoff: time.Millisecond,
	})

	state := NewState()
	cmd := NewCommands(state, httpClient, Config{
		BaseURL:         srv.URL,
		AccountID:       "acc1",
		DisplayName:     "Bot",
		Platform:        "WIN",
		MaxPatchRetries: 3,
		MinPatchBackoff: time.Millisecond,
		MaxPatchBackoff: time.Millisecond,
	})
	cmd.sleep = func(time.Duration) {}

	if err := cmd.SyncCurrentParty(context.Background()); err != nil {
		t.Fatalf("sync current: %v", err)
	}

	if err := cmd.SetCustomKey(context.Background(), "new-key"); err != nil {
		t.Fatalf("set custom key: %v", err)
	}

	if patchCalls != 2 {
		t.Fatalf("expected 2 patch calls, got %d", patchCalls)
	}

	if len(patchRevisions) != 2 || patchRevisions[0] != 10 || patchRevisions[1] != 11 {
		t.Fatalf("unexpected patch revisions: %v", patchRevisions)
	}

	snap := state.Snapshot()
	if snap == nil {
		t.Fatalf("expected party snapshot")
	}

	if snap.Revision != 12 {
		t.Fatalf("expected revision 12, got %d", snap.Revision)
	}

	if snap.CustomKey != "new-key" {
		t.Fatalf("expected custom key new-key, got %s", snap.CustomKey)
	}
}

func TestEnsurePartyCreatesWhenMissing(t *testing.T) {
	var createCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/acc1") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties") && r.Method == http.MethodPost {
			createCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(partyJSONWith(1, 1, "", "NotReady")))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	state := NewState()
	cmd := NewCommands(state, httpClient, Config{BaseURL: srv.URL, AccountID: "acc1", DisplayName: "Bot"})

	party, err := cmd.EnsureParty(context.Background())
	if err != nil {
		t.Fatalf("ensure party: %v", err)
	}

	if createCalls != 1 {
		t.Fatalf("expected one create call, got %d", createCalls)
	}

	if party == nil || party.ID == "" {
		t.Fatalf("expected created party")
	}
}

func TestLeavePartyAutoCreatesByDefault(t *testing.T) {
	var mu sync.Mutex
	currentPartyID := "p1"
	createCalls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/acc1") {
			mu.Lock()
			id := currentPartyID
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			if id == "" {
				_, _ = w.Write([]byte(`{"current":[]}`))
				return
			}

			_, _ = w.Write([]byte(`{"current":[` + partyJSONWith(5, 2, "", "NotReady") + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1/members/acc1") && r.Method == http.MethodDelete {
			mu.Lock()
			currentPartyID = ""
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties") && r.Method == http.MethodPost {
			mu.Lock()
			createCalls++
			currentPartyID = "p2"
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			payload := strings.ReplaceAll(partyJSONWith(8, 3, "", "NotReady"), `"p1"`, `"p2"`)
			_, _ = w.Write([]byte(payload))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	state := NewState()
	cmd := NewCommands(state, httpClient, Config{BaseURL: srv.URL, AccountID: "acc1", DisplayName: "Bot"})

	if err := cmd.SyncCurrentParty(context.Background()); err != nil {
		t.Fatalf("sync current: %v", err)
	}

	if err := cmd.LeaveParty(context.Background()); err != nil {
		t.Fatalf("leave party: %v", err)
	}

	if createCalls != 1 {
		t.Fatalf("expected one create call, got %d", createCalls)
	}

	snapshot := state.Snapshot()
	if snapshot == nil || snapshot.ID != "p2" {
		t.Fatalf("expected recreated party p2, got %#v", snapshot)
	}
}

func TestJoinPartyByMemberIDResolvesParty(t *testing.T) {
	var joinedPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/friend1") {
			w.Header().Set("Content-Type", "application/json")
			payload := strings.ReplaceAll(partyJSONWith(4, 2, "", "NotReady"), `"p1"`, `"p9"`)
			_, _ = w.Write([]byte(`{"current":[` + payload + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p9/members/acc1/join") && r.Method == http.MethodPost {
			joinedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p9") && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			payload := strings.ReplaceAll(partyJSONWith(5, 2, "", "NotReady"), `"p1"`, `"p9"`)
			_, _ = w.Write([]byte(payload))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	state := NewState()
	cmd := NewCommands(state, httpClient, Config{BaseURL: srv.URL, AccountID: "acc1", DisplayName: "Bot"})

	if err := cmd.JoinPartyByMemberID(context.Background(), "friend1"); err != nil {
		t.Fatalf("join by member id: %v", err)
	}

	if joinedPath == "" {
		t.Fatalf("expected join endpoint call")
	}

	snapshot := state.Snapshot()
	if snapshot == nil || snapshot.ID != "p9" {
		t.Fatalf("expected party p9 after join")
	}
}

func TestJoinPartyByMemberIDHydratesSelfMemberFromCurrentParty(t *testing.T) {
	var joinedPath string
	selfCurrentCalls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/friend1") && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[` + partyJSONFriendOnly("p9", 4, "friend1") + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p9/members/acc1/join") && r.Method == http.MethodPost {
			joinedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p9") && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(partyJSONFriendOnly("p9", 5, "friend1")))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/user/acc1") && r.Method == http.MethodGet {
			selfCurrentCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[` + partyJSONWithCaptain("p9", 6, "friend1") + `]}`))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	state := NewState()
	cmd := NewCommands(state, httpClient, Config{BaseURL: srv.URL, AccountID: "acc1", DisplayName: "Bot"})

	if err := cmd.JoinPartyByMemberID(context.Background(), "friend1"); err != nil {
		t.Fatalf("join by member id: %v", err)
	}

	if joinedPath == "" {
		t.Fatalf("expected join endpoint call")
	}

	if selfCurrentCalls == 0 {
		t.Fatalf("expected self current-party hydrate call")
	}

	snapshot := state.Snapshot()
	if snapshot == nil || snapshot.ID != "p9" {
		t.Fatalf("expected party p9 after join hydrate")
	}

	if _, ok := snapshot.Members["acc1"]; !ok {
		t.Fatalf("expected hydrated self member in snapshot")
	}
}

func TestJoinPartyByMemberIDSelfOnlyQueryReturnsNotFound(t *testing.T) {
	joinCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/friend1") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.social.party.user_operation_forbidden","errorMessage":"Target accountId [friend1] does not match the authenticated user [acc1]."}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/join") {
			joinCalled = true
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	state := NewState()
	cmd := NewCommands(state, httpClient, Config{BaseURL: srv.URL, AccountID: "acc1", DisplayName: "Bot"})

	err := cmd.JoinPartyByMemberID(context.Background(), "friend1")
	if !errors.Is(err, ErrPartyNotFound) {
		t.Fatalf("expected ErrPartyNotFound, got %v", err)
	}

	if joinCalled {
		t.Fatalf("did not expect join endpoint call")
	}
}

func TestSetReadyRetriesMemberStaleRevision(t *testing.T) {
	var mu sync.Mutex
	memberPatchCalls := 0
	memberPatchRevisions := make([]int64, 0, 2)
	partyFetches := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/acc1") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[` + partyJSONWith(10, 5, "", "NotReady") + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1") && r.Method == http.MethodGet {
			mu.Lock()
			partyFetches++
			fetch := partyFetches
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			if fetch == 1 {
				_, _ = w.Write([]byte(partyJSONWith(11, 6, "", "NotReady")))
				return
			}

			_, _ = w.Write([]byte(partyJSONWith(12, 7, "", "Ready")))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1/members/acc1/meta") && r.Method == http.MethodPatch {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)

			revision := int64(0)
			if rv, ok := payload["revision"].(float64); ok {
				revision = int64(rv)
			}

			mu.Lock()
			memberPatchCalls++
			memberPatchRevisions = append(memberPatchRevisions, revision)
			callNo := memberPatchCalls
			mu.Unlock()

			if callNo == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.social.party.stale_revision","errorMessage":"stale","messageVars":["5","6"]}`))
				return
			}

			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{
		MaxRetries: 0,
		MinBackoff: time.Millisecond,
		MaxBackoff: time.Millisecond,
	})

	state := NewState()
	cmd := NewCommands(state, httpClient, Config{
		BaseURL:         srv.URL,
		AccountID:       "acc1",
		DisplayName:     "Bot",
		Platform:        "WIN",
		MaxPatchRetries: 3,
		MinPatchBackoff: time.Millisecond,
		MaxPatchBackoff: time.Millisecond,
	})
	cmd.sleep = func(time.Duration) {}

	if err := cmd.SyncCurrentParty(context.Background()); err != nil {
		t.Fatalf("sync current: %v", err)
	}

	if err := cmd.SetReady(context.Background(), ReadyStateReady); err != nil {
		t.Fatalf("set ready: %v", err)
	}

	if memberPatchCalls != 2 {
		t.Fatalf("expected 2 member patch calls, got %d", memberPatchCalls)
	}

	if len(memberPatchRevisions) != 2 || memberPatchRevisions[0] != 5 || memberPatchRevisions[1] != 6 {
		t.Fatalf("unexpected member patch revisions: %v", memberPatchRevisions)
	}

	snapshot := state.Snapshot()
	if snapshot == nil {
		t.Fatalf("expected snapshot")
	}

	member := snapshot.Members["acc1"]
	if member.Ready != ReadyStateReady {
		t.Fatalf("expected ready state Ready, got %s", member.Ready)
	}
}

func TestSetReadyHydratesSelfMemberFromCurrentParty(t *testing.T) {
	selfCurrentCalls := 0
	memberPatchCalls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/acc1") && r.Method == http.MethodGet {
			selfCurrentCalls++
			w.Header().Set("Content-Type", "application/json")
			if selfCurrentCalls == 1 {
				_, _ = w.Write([]byte(`{"current":[` + partyJSONFriendOnly("p1", 10, "friend1") + `]}`))
				return
			}

			_, _ = w.Write([]byte(`{"current":[` + partyJSONWith(11, 6, "", "NotReady") + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1/members/acc1/meta") && r.Method == http.MethodPatch {
			memberPatchCalls++
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1") && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(partyJSONWith(12, 7, "", "Ready")))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{
		MaxRetries: 0,
		MinBackoff: time.Millisecond,
		MaxBackoff: time.Millisecond,
	})

	state := NewState()
	cmd := NewCommands(state, httpClient, Config{
		BaseURL:         srv.URL,
		AccountID:       "acc1",
		DisplayName:     "Bot",
		Platform:        "WIN",
		MaxPatchRetries: 1,
		MinPatchBackoff: time.Millisecond,
		MaxPatchBackoff: time.Millisecond,
	})
	cmd.sleep = func(time.Duration) {}

	if err := cmd.SyncCurrentParty(context.Background()); err != nil {
		t.Fatalf("sync current: %v", err)
	}

	if err := cmd.SetReady(context.Background(), ReadyStateReady); err != nil {
		t.Fatalf("set ready after hydrate: %v", err)
	}

	if selfCurrentCalls < 2 {
		t.Fatalf("expected self current-party call for hydrate, got %d", selfCurrentCalls)
	}

	if memberPatchCalls == 0 {
		t.Fatalf("expected member patch call")
	}
}

func TestSetOutfitRetriesMemberStaleRevision(t *testing.T) {
	var mu sync.Mutex
	memberPatchCalls := 0
	memberPatchRevisions := make([]int64, 0, 2)
	partyFetches := 0
	lastUpdate := map[string]any{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/acc1") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[` + partyJSONWith(10, 5, "", "NotReady") + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1") && r.Method == http.MethodGet {
			mu.Lock()
			partyFetches++
			fetch := partyFetches
			mu.Unlock()

			if fetch == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(partyJSONWith(11, 6, "", "NotReady")))
				return
			}

			updated := strings.ReplaceAll(partyJSONWith(12, 7, "", "NotReady"), "CID_A_001", "CID_NEW_123")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(updated))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1/members/acc1/meta") && r.Method == http.MethodPatch {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)

			revision := int64(0)
			if rv, ok := payload["revision"].(float64); ok {
				revision = int64(rv)
			}

			update, _ := payload["update"].(map[string]any)
			mu.Lock()
			memberPatchCalls++
			memberPatchRevisions = append(memberPatchRevisions, revision)
			lastUpdate = update
			callNo := memberPatchCalls
			mu.Unlock()

			if callNo == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.social.party.stale_revision","errorMessage":"stale","messageVars":["5","6"]}`))
				return
			}

			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{
		MaxRetries: 0,
		MinBackoff: time.Millisecond,
		MaxBackoff: time.Millisecond,
	})

	state := NewState()
	cmd := NewCommands(state, httpClient, Config{
		BaseURL:         srv.URL,
		AccountID:       "acc1",
		DisplayName:     "Bot",
		Platform:        "WIN",
		MaxPatchRetries: 3,
		MinPatchBackoff: time.Millisecond,
		MaxPatchBackoff: time.Millisecond,
	})
	cmd.sleep = func(time.Duration) {}

	if err := cmd.SyncCurrentParty(context.Background()); err != nil {
		t.Fatalf("sync current: %v", err)
	}

	if err := cmd.SetOutfit(context.Background(), "CID_NEW_123"); err != nil {
		t.Fatalf("set outfit: %v", err)
	}

	if memberPatchCalls != 2 {
		t.Fatalf("expected 2 member patch calls, got %d", memberPatchCalls)
	}

	if len(memberPatchRevisions) != 2 || memberPatchRevisions[0] != 5 || memberPatchRevisions[1] != 6 {
		t.Fatalf("unexpected member patch revisions: %v", memberPatchRevisions)
	}

	if _, ok := lastUpdate["Default:AthenaCosmeticLoadout_j"]; !ok {
		t.Fatalf("expected cosmetic loadout patch key")
	}

	if _, ok := lastUpdate["Default:MpLoadout_j"]; !ok {
		t.Fatalf("expected mp loadout patch key")
	}

	snapshot := state.Snapshot()
	if snapshot == nil {
		t.Fatalf("expected snapshot")
	}

	member := snapshot.Members["acc1"]
	if member.Loadout.Character != "AthenaCharacter:CID_NEW_123" {
		t.Fatalf("expected updated outfit, got %s", member.Loadout.Character)
	}
}

func TestPromoteMemberSuccess(t *testing.T) {
	var promoteCalls int
	var promotePath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/acc1") && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[` + partyJSONWithCaptain("p1", 10, "acc1") + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1/members/friend1/promote") && r.Method == http.MethodPost {
			promoteCalls++
			promotePath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1") && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(partyJSONWithCaptain("p1", 11, "friend1")))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	state := NewState()
	cmd := NewCommands(state, httpClient, Config{BaseURL: srv.URL, AccountID: "acc1", DisplayName: "Bot"})

	if err := cmd.SyncCurrentParty(context.Background()); err != nil {
		t.Fatalf("sync current: %v", err)
	}

	if err := cmd.PromoteMember(context.Background(), "p1", "friend1"); err != nil {
		t.Fatalf("promote member: %v", err)
	}

	if promoteCalls != 1 {
		t.Fatalf("expected one promote call, got %d", promoteCalls)
	}

	if promotePath == "" {
		t.Fatalf("expected promote path")
	}

	snapshot := state.Snapshot()
	if snapshot == nil {
		t.Fatalf("expected snapshot")
	}

	if snapshot.CaptainID != "friend1" {
		t.Fatalf("expected captain friend1, got %s", snapshot.CaptainID)
	}
}

func TestPromoteMemberForbiddenMapsNotCaptain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/acc1") && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[` + partyJSONWithCaptain("p1", 10, "acc1") + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1/members/friend1/promote") && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.social.party.party_change_forbidden","errorMessage":"forbidden"}`))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	state := NewState()
	cmd := NewCommands(state, httpClient, Config{BaseURL: srv.URL, AccountID: "acc1", DisplayName: "Bot"})

	if err := cmd.SyncCurrentParty(context.Background()); err != nil {
		t.Fatalf("sync current: %v", err)
	}

	err := cmd.PromoteMember(context.Background(), "p1", "friend1")
	if !errors.Is(err, ErrNotCaptain) {
		t.Fatalf("expected ErrNotCaptain, got %v", err)
	}
}

func TestPromoteMemberNotFoundMapsMemberNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/acc1") && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[` + partyJSONWithCaptain("p1", 10, "acc1") + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1/members/missing/promote") && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errorCode":"errors.com.epicgames.social.party.party_member_not_found","errorMessage":"not found"}`))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	httpClient := transporthttp.NewClient(srv.Client(), nil, transporthttp.Config{MaxRetries: 0})
	state := NewState()
	cmd := NewCommands(state, httpClient, Config{BaseURL: srv.URL, AccountID: "acc1", DisplayName: "Bot"})

	if err := cmd.SyncCurrentParty(context.Background()); err != nil {
		t.Fatalf("sync current: %v", err)
	}

	err := cmd.PromoteMember(context.Background(), "p1", "missing")
	if !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("expected ErrMemberNotFound, got %v", err)
	}
}

func partyJSONWith(revision int64, memberRevision int64, customKey string, readiness string) string {
	joinedAt := time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	lobbyState, _ := json.Marshal(map[string]any{
		"LobbyState": map[string]any{
			"gameReadiness": readiness,
		},
	})
	loadout, _ := json.Marshal(map[string]any{
		"AthenaCosmeticLoadout": map[string]any{
			"characterPrimaryAssetId": "AthenaCharacter:CID_A_001",
		},
	})

	payload := map[string]any{
		"id":       "p1",
		"revision": revision,
		"meta": map[string]any{
			"Default:CustomMatchKey_s": customKey,
			"Default:RegionId_s":       "EU",
		},
		"members": []map[string]any{
			{
				"account_id": "acc1",
				"account_dn": "Bot",
				"role":       "CAPTAIN",
				"joined_at":  joinedAt,
				"revision":   memberRevision,
				"meta": map[string]any{
					"Default:LobbyState_j":            string(lobbyState),
					"Default:AthenaCosmeticLoadout_j": string(loadout),
				},
			},
		},
	}

	raw, _ := json.Marshal(payload)
	return string(raw)
}

func partyJSONWithCaptain(id string, revision int64, captainID string) string {
	joinedAt := time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	roleAcc1 := "MEMBER"
	roleFriend := "MEMBER"
	if captainID == "acc1" {
		roleAcc1 = "CAPTAIN"
	}

	if captainID == "friend1" {
		roleFriend = "CAPTAIN"
	}

	payload := map[string]any{
		"id":       id,
		"revision": revision,
		"meta": map[string]any{
			"Default:RegionId_s": "EU",
		},
		"members": []map[string]any{
			{
				"account_id": "acc1",
				"account_dn": "Bot",
				"role":       roleAcc1,
				"joined_at":  joinedAt,
				"revision":   5,
				"meta": map[string]any{
					"Default:LobbyState_j":            `{"LobbyState":{"gameReadiness":"NotReady"}}`,
					"Default:AthenaCosmeticLoadout_j": `{"AthenaCosmeticLoadout":{"characterPrimaryAssetId":"AthenaCharacter:CID_A_001"}}`,
				},
			},
			{
				"account_id": "friend1",
				"account_dn": "Friend",
				"role":       roleFriend,
				"joined_at":  joinedAt,
				"revision":   3,
				"meta": map[string]any{
					"Default:LobbyState_j":            `{"LobbyState":{"gameReadiness":"NotReady"}}`,
					"Default:AthenaCosmeticLoadout_j": `{"AthenaCosmeticLoadout":{"characterPrimaryAssetId":"AthenaCharacter:CID_B_001"}}`,
				},
			},
		},
	}

	raw, _ := json.Marshal(payload)
	return string(raw)
}

func partyJSONFriendOnly(id string, revision int64, captainID string) string {
	joinedAt := time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	role := "MEMBER"
	if captainID == "friend1" {
		role = "CAPTAIN"
	}

	payload := map[string]any{
		"id":       id,
		"revision": revision,
		"meta": map[string]any{
			"Default:RegionId_s": "EU",
		},
		"members": []map[string]any{
			{
				"account_id": "friend1",
				"account_dn": "Friend",
				"role":       role,
				"joined_at":  joinedAt,
				"revision":   3,
				"meta": map[string]any{
					"Default:LobbyState_j":            `{"LobbyState":{"gameReadiness":"NotReady"}}`,
					"Default:AthenaCosmeticLoadout_j": `{"AthenaCosmeticLoadout":{"characterPrimaryAssetId":"AthenaCharacter:CID_B_001"}}`,
				},
			},
		},
	}

	raw, _ := json.Marshal(payload)
	return string(raw)
}
