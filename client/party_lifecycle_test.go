package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ceskypane/fngo/auth"
	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/party"
	transporthttp "github.com/ceskypane/fngo/transport/http"
	"github.com/ceskypane/fngo/xmpp"
	"github.com/gorilla/websocket"
)

func TestClientReconnectTriggersSinglePartyResync(t *testing.T) {
	var userCalls atomic.Int32

	partySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user/acc1") {
			userCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"current":[` + lifecyclePartyJSON("p1", 10) + `]}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "/parties/p1") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(lifecyclePartyJSON("p1", 10)))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer partySrv.Close()

	upgrader := websocket.Upgrader{}
	var connCount atomic.Int32

	xmppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		count := connCount.Add(1)
		if count == 1 {
			time.Sleep(100 * time.Millisecond)
			_ = conn.Close()
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

	tokenStore := auth.NewMemoryTokenStore()
	if err := tokenStore.Save(context.Background(), auth.TokenSet{
		AccessToken: "access-token",
		AccountID:   "acc1",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("seed token store: %v", err)
	}

	endpoint := "ws" + strings.TrimPrefix(xmppSrv.URL, "http")
	client, err := NewClient(Config{
		EventBuffer: 128,
		TokenStore:  tokenStore,
		HTTPClient:  partySrv.Client(),
		HTTP:        transporthttp.Config{MaxRetries: 0},
		Party: party.Config{
			BaseURL:                     partySrv.URL,
			AccountID:                   "acc1",
			DisplayName:                 "Bot",
			DisableAutoCreateAfterLeave: true,
		},
		EnableXMPP: true,
		XMPP: xmpp.Config{
			Endpoint:          endpoint,
			EnableHandshake:   false,
			MinReconnectDelay: 50 * time.Millisecond,
			MaxReconnectDelay: 50 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Login(ctx); err != nil {
		t.Fatalf("login: %v", err)
	}

	defer func() {
		_ = client.Logout(context.Background())
	}()

	connected := 0
	deadline := time.After(4 * time.Second)
	for connected < 2 {
		select {
		case <-deadline:
			t.Fatalf("expected reconnect with second xmpp.connected event")
		case evt := <-client.Events():
			if evt.Name() == events.EventXMPPConnected {
				connected++
			}
		}
	}

	waitDeadline := time.Now().Add(2 * time.Second)
	for userCalls.Load() < 2 && time.Now().Before(waitDeadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := userCalls.Load(); got != 2 {
		t.Fatalf("expected 2 /user calls (login + reconnect resync), got %d", got)
	}
}

func lifecyclePartyJSON(id string, revision int64) string {
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
				"role":       "CAPTAIN",
				"joined_at":  time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
				"revision":   1,
				"meta": map[string]any{
					"Default:LobbyState_j": `{"LobbyState":{"gameReadiness":"NotReady"}}`,
				},
			},
		},
	}

	raw, _ := json.Marshal(payload)
	return string(raw)
}
