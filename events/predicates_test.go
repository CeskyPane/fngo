package events

import (
	"testing"
	"time"
)

func TestPartyPredicates(t *testing.T) {
	evt := PartyUpdated{
		Base:      Base{At: time.Now().UTC()},
		PartyID:   "party-1",
		Playlist:  "playlist_defaultsquad",
		CustomKey: "abc123",
	}

	if !PartyUpdatedForID("party-1")(evt) {
		t.Fatalf("expected party id predicate to match")
	}

	if !PartyPlaylistEquals("party-1", "playlist_defaultsquad")(evt) {
		t.Fatalf("expected playlist predicate to match")
	}

	if !PartyCustomKeyEquals("party-1", "abc123")(evt) {
		t.Fatalf("expected custom key predicate to match")
	}

	if PartyUpdatedForID("party-2")(evt) {
		t.Fatalf("unexpected match for non-matching party id")
	}
}
