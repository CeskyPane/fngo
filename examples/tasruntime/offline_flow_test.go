package tasruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ceskypane/fngo/auth"
	"github.com/ceskypane/fngo/auth/epic"
	clientpkg "github.com/ceskypane/fngo/client"
	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/matchmaking"
	"github.com/ceskypane/fngo/party"
	"github.com/ceskypane/fngo/xmpp"
	"github.com/gorilla/websocket"
)

type partyState struct {
	ID             string
	Revision       int64
	Meta           map[string]string
	MemberRevision int64
	MemberMeta     map[string]string
}

func TestOfflineFlowLoginEnsureJoinPlaylistReady(t *testing.T) {
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		grantType := r.Form.Get("grant_type")
		if grantType != "device_auth" && grantType != "refresh_token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"access-token",
			"refresh_token":"refresh-token",
			"token_type":"bearer",
			"expires_in":3600,
			"refresh_expires":7200,
			"account_id":"acc1",
			"displayName":"Bot"
		}`))
	}))
	defer oauthSrv.Close()

	var mu sync.Mutex
	parties := map[string]*partyState{
		"p-join": {
			ID:             "p-join",
			Revision:       20,
			Meta:           map[string]string{"Default:RegionId_s": "EU", "Default:SelectedIsland_j": `{"SelectedIsland":{"linkId":{"mnemonic":"playlist_defaultsquad","version":-1}}}`},
			MemberRevision: 3,
			MemberMeta: map[string]string{
				"Default:LobbyState_j":            `{"LobbyState":{"gameReadiness":"NotReady","readyInputType":"Count"}}`,
				"Default:AthenaCosmeticLoadout_j": `{"AthenaCosmeticLoadout":{"characterPrimaryAssetId":"AthenaCharacter:CID_A_001"}}`,
			},
		},
	}
	currentPartyID := ""
	readySeen := false

	partySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		mu.Lock()
		defer mu.Unlock()

		if strings.HasSuffix(path, "/user/acc1") && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			if currentPartyID == "" {
				_, _ = w.Write([]byte(`{"current":[]}`))
				return
			}

			current := parties[currentPartyID]
			_, _ = w.Write([]byte(`{"current":[` + encodePartyJSON(current) + `]}`))
			return
		}

		if strings.HasSuffix(path, "/parties") && r.Method == http.MethodPost {
			created := &partyState{
				ID:             "p-created",
				Revision:       1,
				Meta:           map[string]string{"Default:RegionId_s": "EU"},
				MemberRevision: 1,
				MemberMeta: map[string]string{
					"Default:LobbyState_j":            `{"LobbyState":{"gameReadiness":"NotReady","readyInputType":"Count"}}`,
					"Default:AthenaCosmeticLoadout_j": `{"AthenaCosmeticLoadout":{"characterPrimaryAssetId":"AthenaCharacter:CID_A_001"}}`,
				},
			}
			parties[created.ID] = created
			currentPartyID = created.ID
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(encodePartyJSON(created)))
			return
		}

		if strings.Contains(path, "/members/acc1/join") && r.Method == http.MethodPost {
			segments := strings.Split(path, "/")
			partyID := segments[len(segments)-4]
			if _, ok := parties[partyID]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			currentPartyID = partyID
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.Contains(path, "/members/acc1") && r.Method == http.MethodDelete {
			currentPartyID = ""
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.Contains(path, "/members/acc1/meta") && r.Method == http.MethodPatch {
			segments := strings.Split(path, "/")
			partyID := segments[len(segments)-4]
			ps := parties[partyID]
			if ps == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			var payload struct {
				Update map[string]string `json:"update"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			for key, value := range payload.Update {
				ps.MemberMeta[key] = value
				if key == "Default:LobbyState_j" && strings.Contains(value, `"Ready"`) {
					readySeen = true
				}
			}

			ps.MemberRevision++
			ps.Revision++
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.HasPrefix(path, "/party/api/v1/Fortnite/parties/") && r.Method == http.MethodPatch {
			segments := strings.Split(path, "/")
			partyID := segments[len(segments)-1]
			ps := parties[partyID]
			if ps == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			var payload struct {
				Meta struct {
					Update map[string]string `json:"update"`
				} `json:"meta"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			for key, value := range payload.Meta.Update {
				ps.Meta[key] = value
			}
			ps.Revision++
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if strings.HasPrefix(path, "/party/api/v1/Fortnite/parties/") && r.Method == http.MethodGet {
			segments := strings.Split(path, "/")
			partyID := segments[len(segments)-1]
			ps := parties[partyID]
			if ps == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(encodePartyJSON(ps)))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer partySrv.Close()

	upgrader := websocket.Upgrader{}
	xmppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer xmppSrv.Close()

	endpoint := "ws" + strings.TrimPrefix(xmppSrv.URL, "http")
	client, err := clientpkg.NewClient(clientpkg.Config{
		EventBuffer: 256,
		DeviceAuth:  authDevice("acc1"),
		OAuth:       epicOAuth(oauthSrv.URL+"/oauth/token", oauthSrv.Client()),
		HTTPClient:  partySrv.Client(),
		Party: party.Config{
			BaseURL:                     partySrv.URL + "/party/api/v1/Fortnite",
			AccountID:                   "acc1",
			DisplayName:                 "Bot",
			DisableAutoCreateAfterLeave: true,
		},
		EnableXMPP: true,
		XMPP: xmpp.Config{
			Endpoint:          endpoint,
			EnableHandshake:   false,
			MinReconnectDelay: 100 * time.Millisecond,
			MaxReconnectDelay: 100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Login(ctx); err != nil {
		t.Fatalf("login: %v", err)
	}

	defer func() {
		_ = client.Logout(context.Background())
	}()

	if err := waitForAfterAction(ctx, client, func(evt events.Event) bool {
		pu, ok := evt.(events.PartyUpdated)
		return ok && pu.PartyID == "p-join"
	}, func() error {
		return client.JoinParty(ctx, "p-join")
	}); err != nil {
		t.Fatalf("wait party joined: %v", err)
	}

	if err := waitForAfterAction(ctx, client, func(evt events.Event) bool {
		pu, ok := evt.(events.PartyUpdated)
		return ok && pu.PartyID == "p-join" && strings.Contains(strings.ToLower(pu.Playlist), "playlist")
	}, func() error {
		return client.SetPlaylist(ctx, matchmaking.PlaylistRequest{
			PlaylistID: "playlist_defaultsquad",
			Region:     "EU",
			TeamFill:   true,
		})
	}); err != nil {
		t.Fatalf("wait playlist updated: %v", err)
	}

	if err := waitForAfterAction(ctx, client, func(evt events.Event) bool {
		pu, ok := evt.(events.PartyUpdated)
		return ok && pu.PartyID == "p-join"
	}, func() error {
		return client.SetReady(ctx, party.ReadyStateReady)
	}); err != nil {
		t.Fatalf("wait ready update event: %v", err)
	}

	mu.Lock()
	seenReady := readySeen
	mu.Unlock()
	if !seenReady {
		t.Fatalf("expected member ready patch to be applied")
	}
}

func encodePartyJSON(ps *partyState) string {
	payload := map[string]any{
		"id":       ps.ID,
		"revision": ps.Revision,
		"meta":     ps.Meta,
		"members": []map[string]any{
			{
				"account_id": "acc1",
				"account_dn": "Bot",
				"role":       "CAPTAIN",
				"joined_at":  time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
				"revision":   ps.MemberRevision,
				"meta":       ps.MemberMeta,
			},
		},
	}

	raw, _ := json.Marshal(payload)
	return string(raw)
}

func authDevice(accountID string) auth.DeviceAuth {
	return auth.DeviceAuth{AccountID: accountID, DeviceID: "dev-1", Secret: "secret-1"}
}

func epicOAuth(tokenURL string, httpClient *http.Client) epic.Config {
	return epic.Config{
		TokenURL:     tokenURL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		HTTPClient:   httpClient,
		MaxRetries:   0,
	}
}

func waitForAfterAction(
	ctx context.Context,
	client *clientpkg.Client,
	predicate events.Predicate,
	action func() error,
) error {
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	waitErrCh := make(chan error, 1)
	go func() {
		_, err := client.WaitFor(waitCtx, predicate)
		waitErrCh <- err
	}()

	time.Sleep(10 * time.Millisecond)

	if err := action(); err != nil {
		return err
	}

	select {
	case <-waitCtx.Done():
		return waitCtx.Err()
	case err := <-waitErrCh:
		return err
	}
}
