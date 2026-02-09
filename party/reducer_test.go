package party

import (
	"testing"
	"time"
)

func TestApplyEventMemberJoined(t *testing.T) {
	state := NewState()
	now := time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC)

	err := state.ApplyEvent(ReducerEvent{
		Kind:     EventMemberJoined,
		PartyID:  "party-1",
		Revision: 10,
		At:       now,
		Member: Member{
			AccountID:   "acc-1",
			DisplayName: "BotOne",
			Ready:       ReadyStateReady,
		},
	})
	if err != nil {
		t.Fatalf("apply event: %v", err)
	}

	snap := state.Snapshot()
	if snap == nil {
		t.Fatalf("expected snapshot")
	}

	if snap.ID != "party-1" {
		t.Fatalf("unexpected party id: %s", snap.ID)
	}

	if snap.Revision != 10 {
		t.Fatalf("unexpected revision: %d", snap.Revision)
	}

	member, ok := snap.Members["acc-1"]
	if !ok {
		t.Fatalf("expected member in party")
	}

	if member.DisplayName != "BotOne" {
		t.Fatalf("unexpected member display name: %s", member.DisplayName)
	}
}
