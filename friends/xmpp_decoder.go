package friends

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/xmpp"
)

type XMPPDecoder struct {
	bus    *events.Bus
	selfID string
	now    func() time.Time
}

func NewXMPPDecoder(bus *events.Bus) *XMPPDecoder {
	if bus == nil {
		bus = events.NewBus()
	}

	return &XMPPDecoder{
		bus: bus,
		now: time.Now,
	}
}

func (d *XMPPDecoder) SetIdentity(accountID string) {
	d.selfID = trimSpace(accountID)
}

func (d *XMPPDecoder) DispatchStanza(_ context.Context, stanza xmpp.Stanza) {
	if stanza.Kind != xmpp.StanzaKindMessage {
		return
	}

	if trimSpace(stanza.Body) == "" {
		return
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(stanza.Body), &payload); err != nil {
		return
	}

	msgType := trimSpace(asString(payload["type"]))
	if msgType == "" {
		return
	}

	at := d.now().UTC()

	switch msgType {
	case "com.epicgames.friends.core.apiobjects.Friend":
		d.handleFriendObject(at, payload)
	case "FRIENDSHIP_REMOVE":
		d.handleFriendshipRemove(at, payload)
	}
}

func (d *XMPPDecoder) handleFriendObject(at time.Time, payload map[string]any) {
	raw, ok := payload["payload"].(map[string]any)
	if !ok {
		return
	}

	status := strings.ToUpper(trimSpace(asString(raw["status"])))
	accountID := trimSpace(asString(raw["accountId"]))
	direction := strings.ToUpper(trimSpace(asString(raw["direction"])))
	if accountID == "" || status == "" {
		return
	}

	switch status {
	case "ACCEPTED":
		_ = d.bus.Emit(events.FriendAdded{
			Base:      events.Base{At: at},
			AccountID: accountID,
		})
	case "PENDING":
		if direction == "INBOUND" {
			_ = d.bus.Emit(events.FriendRequestReceived{
				Base:      events.Base{At: at},
				AccountID: accountID,
			})
		}
	}
}

func (d *XMPPDecoder) handleFriendshipRemove(at time.Time, payload map[string]any) {
	reason := strings.ToUpper(trimSpace(asString(payload["reason"])))
	from := trimSpace(asString(payload["from"]))
	to := trimSpace(asString(payload["to"]))
	other := from
	if d.selfID != "" && strings.EqualFold(from, d.selfID) {
		other = to
	} else if trimSpace(other) == "" {
		other = to
	}

	if other == "" || reason == "" {
		return
	}

	switch reason {
	case "REJECTED":
		_ = d.bus.Emit(events.FriendRequestDeclined{
			Base:      events.Base{At: at},
			AccountID: other,
		})
	case "DELETED":
		_ = d.bus.Emit(events.FriendRemoved{
			Base:      events.Base{At: at},
			AccountID: other,
			Reason:    "DELETED",
		})
	}
}

func asString(v any) string {
	if v == nil {
		return ""
	}

	s, ok := v.(string)
	if ok {
		return s
	}

	return ""
}
