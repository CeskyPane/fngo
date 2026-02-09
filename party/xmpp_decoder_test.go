package party

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/xmpp"
	"github.com/gorilla/websocket"
)

type staticTokenSource struct {
	token string
}

func (s staticTokenSource) AccessToken(context.Context) (string, error) {
	return s.token, nil
}

func TestXMPPDecoderEmitsPartyEventsInOrder(t *testing.T) {
	upgrader := websocket.Upgrader{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		memberMeta := map[string]any{
			"Default:LobbyState_j":            `{"LobbyState":{"gameReadiness":"NotReady"}}`,
			"Default:AthenaCosmeticLoadout_j": `{"AthenaCosmeticLoadout":{"characterPrimaryAssetId":"AthenaCharacter:CID_A_001"}}`,
		}

		frames := []string{
			buildMessageStanza(map[string]any{
				"type":       "com.epicgames.social.party.notification.v0.MEMBER_JOINED",
				"party_id":   "p1",
				"account_id": "acc1",
				"account_dn": "PlayerOne",
				"role":       "MEMBER",
				"revision":   1,
				"meta":       memberMeta,
			}),
			buildMessageStanza(map[string]any{
				"type":       "com.epicgames.social.party.notification.v0.MEMBER_STATE_UPDATED",
				"party_id":   "p1",
				"account_id": "acc1",
				"revision":   2,
				"member_state_updated": map[string]any{
					"Default:LobbyState_j": `{"LobbyState":{"gameReadiness":"Ready","readyInputType":"MouseAndKeyboard"}}`,
				},
			}),
			buildMessageStanza(map[string]any{
				"type":       "com.epicgames.social.party.notification.v0.MEMBER_NEW_CAPTAIN",
				"party_id":   "p1",
				"account_id": "acc1",
				"revision":   3,
			}),
		}

		for _, frame := range frames {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(frame))
		}

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	bus := events.NewBus()
	sub, err := bus.Subscribe(256)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	state := NewState()
	decoder := NewXMPPDecoder(bus, state)

	endpoint := "ws" + strings.TrimPrefix(srv.URL, "http")
	xmppClient := xmpp.NewClient(xmpp.Config{
		Endpoint:          endpoint,
		EnableHandshake:   false,
		MinReconnectDelay: time.Second,
		MaxReconnectDelay: time.Second,
		StanzaDispatcher:  decoder,
	}, bus, staticTokenSource{token: "token"}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := xmppClient.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		_ = xmppClient.Close(context.Background())
	}()

	want := []events.Name{
		events.EventPartyMemberJoined,
		events.EventPartyMemberUpdated,
		events.EventPartyCaptainChanged,
	}

	got := make([]events.Name, 0, len(want))
	deadline := time.After(3 * time.Second)
	for len(got) < len(want) {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for ordered party events: got=%v", got)
		case evt := <-sub.C:
			for _, expected := range want {
				if evt.Name() == expected {
					got = append(got, evt.Name())
					break
				}
			}
		}
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected event order: got=%v want=%v", got, want)
	}

	snapshot := state.Snapshot()
	if snapshot == nil {
		t.Fatalf("expected state snapshot")
	}

	if snapshot.ID != "p1" {
		t.Fatalf("unexpected party id: %s", snapshot.ID)
	}

	if len(snapshot.Members) != 1 {
		t.Fatalf("expected one member, got %d", len(snapshot.Members))
	}

	member := snapshot.Members["acc1"]
	if member.Ready != ReadyStateReady {
		t.Fatalf("expected member ready state Ready, got %s", member.Ready)
	}

	if snapshot.CaptainID != "acc1" {
		t.Fatalf("expected captain acc1, got %s", snapshot.CaptainID)
	}
}

func TestXMPPDecoderMemberNewCaptainUpdatesStateAndEmitsEvent(t *testing.T) {
	bus := events.NewBus()
	sub, err := bus.Subscribe(32)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	state := NewState()
	decoder := NewXMPPDecoder(bus, state)

	seed := buildMessageStanza(map[string]any{
		"type":       "com.epicgames.social.party.notification.v0.MEMBER_JOINED",
		"party_id":   "p-promote",
		"account_id": "member-a",
		"account_dn": "MemberA",
		"role":       "MEMBER",
		"revision":   1,
		"meta": map[string]any{
			"Default:LobbyState_j": `{"LobbyState":{"gameReadiness":"NotReady"}}`,
		},
	})

	decoder.DispatchStanza(context.Background(), xmpp.Stanza{
		Kind: xmpp.StanzaKindMessage,
		Body: extractBody(seed),
		Raw:  seed,
	})

	promote := buildMessageStanza(map[string]any{
		"type":       "com.epicgames.social.party.notification.v0.MEMBER_NEW_CAPTAIN",
		"party_id":   "p-promote",
		"account_id": "member-a",
		"revision":   2,
	})

	decoder.DispatchStanza(context.Background(), xmpp.Stanza{
		Kind: xmpp.StanzaKindMessage,
		Body: extractBody(promote),
		Raw:  promote,
	})

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()

	foundCaptainChanged := false
	for !foundCaptainChanged {
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for PartyCaptainChanged")
		case evt := <-sub.C:
			change, ok := evt.(events.PartyCaptainChanged)
			if !ok {
				continue
			}

			if change.PartyID == "p-promote" && change.CaptainID == "member-a" {
				foundCaptainChanged = true
			}
		}
	}

	snapshot := state.Snapshot()
	if snapshot == nil {
		t.Fatalf("expected snapshot")
	}

	if snapshot.CaptainID != "member-a" {
		t.Fatalf("expected captain member-a, got %s", snapshot.CaptainID)
	}
}

func buildMessageStanza(payload map[string]any) string {
	raw, _ := json.Marshal(payload)
	return `<message from='test@prod.ol.epicgames.com' type='chat'><body>` + string(raw) + `</body></message>`
}

func extractBody(stanza string) string {
	start := strings.Index(stanza, "<body>")
	end := strings.Index(stanza, "</body>")
	if start < 0 || end < 0 || end <= start {
		return ""
	}

	return stanza[start+len("<body>") : end]
}
