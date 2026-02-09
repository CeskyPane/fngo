package party

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/xmpp"
)

const partyNotificationPrefix = "com.epicgames.social.party.notification.v0."

type XMPPDecoder struct {
	bus   *events.Bus
	state *State
	now   func() time.Time
}

func NewXMPPDecoder(bus *events.Bus, state *State) *XMPPDecoder {
	if bus == nil {
		bus = events.NewBus()
	}

	if state == nil {
		state = NewState()
	}

	return &XMPPDecoder{bus: bus, state: state, now: time.Now}
}

func (d *XMPPDecoder) DispatchStanza(_ context.Context, stanza xmpp.Stanza) {
	switch stanza.Kind {
	case xmpp.StanzaKindMessage:
		d.handleMessage(stanza)
	case xmpp.StanzaKindPresence:
		d.handlePresence(stanza)
	case xmpp.StanzaKindIQ:
		d.handleIQ(stanza)
	}
}

func (d *XMPPDecoder) handleMessage(stanza xmpp.Stanza) {
	if strings.TrimSpace(stanza.Body) == "" {
		return
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(stanza.Body), &payload); err != nil {
		return
	}

	notificationType := asString(payload["type"])
	if !strings.HasPrefix(notificationType, partyNotificationPrefix) {
		return
	}

	partyID := asString(payload["party_id"])
	accountID := asString(payload["account_id"])
	revision := asInt64(payload["revision"])
	at := d.now().UTC()

	raw := events.PartyRawNotification{
		Base:             events.Base{At: at},
		NotificationType: notificationType,
		PartyID:          partyID,
		Payload:          cloneAnyMap(payload),
		Raw:              stanza.Raw,
	}

	switch notificationType {
	case partyNotificationPrefix + "MEMBER_JOINED":
		meta := normalizeMetaMapFromAny(payload["meta"])
		if len(meta) == 0 {
			meta = normalizeMetaMapFromAny(payload["member_state_updated"])
		}

		member := Member{
			AccountID:   accountID,
			DisplayName: asString(payload["account_dn"]),
			Role:        asString(payload["role"]),
			Revision:    revision,
			Meta:        meta,
			Ready:       extractReady(meta["Default:LobbyState_j"]),
			Loadout:     extractLoadout(meta["Default:AthenaCosmeticLoadout_j"], meta["Default:FrontendEmote_j"]),
			JoinedAt:    parseTime(asString(payload["joined_at"])),
		}

		_ = d.state.ApplyEvent(ReducerEvent{
			Kind:     EventMemberJoined,
			PartyID:  partyID,
			Revision: revision,
			Member:   member,
			At:       at,
		})

		_ = d.bus.Emit(events.PartyMemberJoined{
			Base:        events.Base{At: at},
			PartyID:     partyID,
			MemberID:    accountID,
			DisplayName: member.DisplayName,
			Revision:    revision,
		})

		if strings.EqualFold(member.Role, "CAPTAIN") {
			_ = d.bus.Emit(events.PartyCaptainChanged{
				Base:      events.Base{At: at},
				PartyID:   partyID,
				CaptainID: accountID,
				Revision:  revision,
			})
		}

		d.emitPartyUpdated(at)
	case partyNotificationPrefix + "MEMBER_STATE_UPDATED":
		updates := normalizeMetaMapFromAny(payload["member_state_updated"])
		removed := toStringSlice(payload["member_state_removed"])
		snapshot := d.state.Snapshot()
		merged := map[string]string{}
		displayName := asString(payload["account_dn"])
		role := asString(payload["role"])
		memberRevision := revision
		if snapshot != nil {
			existing := snapshot.Members[accountID]
			if existing.AccountID != "" {
				merged = cloneMetaStringMap(existing.Meta)
				if displayName == "" {
					displayName = existing.DisplayName
				}
				if role == "" {
					role = existing.Role
				}
				if memberRevision == 0 {
					memberRevision = existing.Revision
				}
			}
		}

		for key, value := range updates {
			merged[key] = value
		}

		for _, key := range removed {
			delete(merged, key)
		}

		member := Member{
			AccountID:   accountID,
			DisplayName: displayName,
			Role:        role,
			Revision:    memberRevision,
			Meta:        updates,
			Ready:       extractReady(merged["Default:LobbyState_j"]),
			Loadout:     extractLoadout(merged["Default:AthenaCosmeticLoadout_j"], merged["Default:FrontendEmote_j"]),
		}

		_ = d.state.ApplyEvent(ReducerEvent{
			Kind:              EventMemberUpdated,
			PartyID:           partyID,
			Revision:          revision,
			Member:            member,
			MemberMetaRemoved: removed,
			At:                at,
		})

		_ = d.bus.Emit(events.PartyMemberUpdated{
			Base:            events.Base{At: at},
			PartyID:         partyID,
			MemberID:        accountID,
			Revision:        revision,
			UpdatedMetaKeys: mapKeys(updates),
		})

		d.emitPartyUpdated(at)
	case partyNotificationPrefix + "MEMBER_LEFT", partyNotificationPrefix + "MEMBER_EXPIRED", partyNotificationPrefix + "MEMBER_DISCONNECTED":
		_ = d.state.ApplyEvent(ReducerEvent{
			Kind:     EventMemberLeft,
			PartyID:  partyID,
			Revision: revision,
			MemberID: accountID,
			At:       at,
		})

		_ = d.bus.Emit(events.PartyMemberLeft{
			Base:     events.Base{At: at},
			PartyID:  partyID,
			MemberID: accountID,
			Revision: revision,
			Reason:   notificationType,
		})

		d.emitPartyUpdated(at)
	case partyNotificationPrefix + "MEMBER_KICKED":
		_ = d.state.ApplyEvent(ReducerEvent{
			Kind:     EventMemberKicked,
			PartyID:  partyID,
			Revision: revision,
			MemberID: accountID,
			At:       at,
		})

		_ = d.bus.Emit(events.PartyMemberKicked{
			Base:     events.Base{At: at},
			PartyID:  partyID,
			MemberID: accountID,
			Revision: revision,
			ActorID:  asString(payload["kicked_by"]),
		})

		d.emitPartyUpdated(at)
	case partyNotificationPrefix + "MEMBER_NEW_CAPTAIN":
		_ = d.state.ApplyEvent(ReducerEvent{
			Kind:      EventPartyUpdated,
			PartyID:   partyID,
			Revision:  revision,
			CaptainID: accountID,
			At:        at,
		})

		_ = d.bus.Emit(events.PartyCaptainChanged{
			Base:      events.Base{At: at},
			PartyID:   partyID,
			CaptainID: accountID,
			Revision:  revision,
		})

		d.emitPartyUpdated(at)
	case partyNotificationPrefix + "PARTY_UPDATED":
		partyUpdates := normalizeMetaMapFromAny(payload["party_state_updated"])
		partyRemoved := toStringSlice(payload["party_state_removed"])
		customKey := ""
		if value, ok := partyUpdates["Default:CustomMatchKey_s"]; ok {
			customKey = value
		}

		playlist := ""
		if value, ok := partyUpdates["Default:SelectedIsland_j"]; ok {
			playlist = extractPlaylist(value)
		}

		_ = d.state.ApplyEvent(ReducerEvent{
			Kind:             EventPartyUpdated,
			PartyID:          partyID,
			Revision:         revision,
			CustomKey:        customKey,
			Playlist:         playlist,
			PartyMetaUpdates: partyUpdates,
			PartyMetaRemoved: partyRemoved,
			At:               at,
		})

		d.emitPartyUpdated(at)
	case partyNotificationPrefix + "PING":
		_ = d.bus.Emit(events.PartyInviteReceived{
			Base:     events.Base{At: at},
			PartyID:  partyID,
			SenderID: asString(payload["pinger_id"]),
			Revision: revision,
		})
	case partyNotificationPrefix + "INITIAL_INTENTION":
		_ = d.bus.Emit(events.PartyJoinRequestReceived{
			Base:        events.Base{At: at},
			PartyID:     partyID,
			RequesterID: asString(payload["requester_id"]),
			Revision:    revision,
		})
	case partyNotificationPrefix + "MEMBER_REQUIRE_CONFIRMATION":
		_ = d.bus.Emit(events.PartyJoinRequestReceived{
			Base:        events.Base{At: at},
			PartyID:     partyID,
			RequesterID: asString(payload["account_id"]),
			Revision:    revision,
		})
	default:
		_ = d.bus.Emit(raw)
		return
	}

	_ = d.bus.Emit(raw)
}

func (d *XMPPDecoder) handlePresence(stanza xmpp.Stanza) {
	type presenceEnvelope struct {
		From   string `xml:"from,attr"`
		Type   string `xml:"type,attr"`
		Status string `xml:"status"`
	}

	var envelope presenceEnvelope
	if err := xml.Unmarshal([]byte(stanza.Raw), &envelope); err != nil {
		return
	}

	statusRaw := strings.TrimSpace(envelope.Status)
	if statusRaw == "" {
		return
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(statusRaw), &payload); err != nil {
		return
	}

	props, ok := payload["Properties"].(map[string]any)
	if !ok {
		return
	}

	joinInfoRaw, ok := props["party.joininfodata.286331153_j"]
	if !ok {
		return
	}

	joinInfo := map[string]any{}
	switch typed := joinInfoRaw.(type) {
	case map[string]any:
		joinInfo = typed
	case string:
		if err := json.Unmarshal([]byte(typed), &joinInfo); err != nil {
			return
		}
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return
		}

		if err := json.Unmarshal(raw, &joinInfo); err != nil {
			return
		}
	}

	friendID := envelope.From
	if idx := strings.Index(friendID, "@"); idx > 0 {
		friendID = friendID[:idx]
	}

	_ = d.bus.Emit(events.PresenceUpdated{
		Base:       events.Base{At: d.now().UTC()},
		AccountID:  friendID,
		PartyID:    asString(joinInfo["partyId"]),
		IsPrivate:  asBool(joinInfo["isPrivate"]),
		IsJoinable: !asBool(joinInfo["isPrivate"]),
		Raw:        statusRaw,
	})
}

func (d *XMPPDecoder) handleIQ(stanza xmpp.Stanza) {
	_ = stanza
}

func (d *XMPPDecoder) emitPartyUpdated(at time.Time) {
	snapshot := d.state.Snapshot()
	if snapshot == nil || snapshot.ID == "" {
		return
	}

	_ = d.bus.Emit(events.PartyUpdated{
		Base:      events.Base{At: at},
		PartyID:   snapshot.ID,
		Revision:  snapshot.Revision,
		CaptainID: snapshot.CaptainID,
		CustomKey: snapshot.CustomKey,
		Playlist:  snapshot.Playlist,
	})
}

func normalizeMetaMapFromAny(v any) map[string]string {
	switch typed := v.(type) {
	case map[string]any:
		return normalizeMetaMap(typed)
	case map[string]string:
		return cloneMetaStringMap(typed)
	case string:
		parsed := decodeJSONMap(typed)
		return normalizeMetaMap(parsed)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return map[string]string{}
		}

		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return map[string]string{}
		}

		return normalizeMetaMap(parsed)
	}
}

func toStringSlice(v any) []string {
	if v == nil {
		return []string{}
	}

	items, ok := v.([]any)
	if !ok {
		asStrings, direct := v.([]string)
		if direct {
			return cloneStringSlice(asStrings)
		}

		return []string{}
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		s := strings.TrimSpace(fmt.Sprint(item))
		if s == "" || s == "<nil>" {
			continue
		}

		out = append(out, s)
	}

	return out
}

func asString(v any) string {
	if v == nil {
		return ""
	}

	s, ok := v.(string)
	if ok {
		return s
	}

	return strings.TrimSpace(fmt.Sprint(v))
}

func asInt64(v any) int64 {
	switch typed := v.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func asBool(v any) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}

	return parsed.UTC()
}

func mapKeys(in map[string]string) []string {
	if len(in) == 0 {
		return []string{}
	}

	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}

	slices.Sort(out)
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}

	raw, err := json.Marshal(in)
	if err != nil {
		return map[string]any{}
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}

	if out == nil {
		return map[string]any{}
	}

	return out
}
