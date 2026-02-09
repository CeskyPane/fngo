package party

import (
	"reflect"
	"testing"
	"time"

	"github.com/ceskypane/fngo/matchmaking"
)

func TestPatchBuilderPreservesTemplateKeys(t *testing.T) {
	builder := NewIntentPatchBuilder("acc1")
	ready := ReadyStateReady
	customKey := "custom-123"
	loadout := Loadout{Character: "AthenaCharacter:CID_Test", Pickaxe: "Pickaxe_Test"}

	snapshot := &Party{
		ID:       "p1",
		Revision: 1,
		Meta:     map[string]string{},
		Members: map[string]Member{
			"acc1": {
				AccountID: "acc1",
				Meta:      map[string]string{},
			},
		},
	}

	built, err := builder.BuildIntents(snapshot, PatchIntent{
		Ready:     &ready,
		CustomKey: &customKey,
		Loadout:   &loadout,
	}, time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("build intents: %v", err)
	}

	if _, ok := built.PartyMetaUpdates["Default:CustomMatchKey_s"]; !ok {
		t.Fatalf("expected custom key update")
	}

	lobby, ok := built.MemberMetaUpdates["Default:LobbyState_j"].(map[string]any)
	if !ok {
		t.Fatalf("expected lobby state json update")
	}

	lobbyState := decodeJSONMapValue(lobby, "LobbyState")
	if lobbyState["gameReadiness"] != "Ready" {
		t.Fatalf("expected readiness Ready, got %v", lobbyState["gameReadiness"])
	}

	if _, hasInput := lobbyState["currentInputType"]; !hasInput {
		t.Fatalf("expected template field currentInputType to be preserved")
	}

	cosmetic, ok := built.MemberMetaUpdates["Default:AthenaCosmeticLoadout_j"].(map[string]any)
	if !ok {
		t.Fatalf("expected cosmetic loadout json update")
	}

	root := decodeJSONMapValue(cosmetic, "AthenaCosmeticLoadout")
	if root["characterPrimaryAssetId"] != "AthenaCharacter:CID_Test" {
		t.Fatalf("expected overridden character, got %v", root["characterPrimaryAssetId"])
	}

	if _, hasBackpack := root["backpackDef"]; !hasBackpack {
		t.Fatalf("expected baseline backpack key from template")
	}
}

func TestPatchBuilderDeterministicForSameInput(t *testing.T) {
	builder := NewIntentPatchBuilder("acc1")
	ready := ReadyStateReady
	playlist := matchmaking.PlaylistRequest{PlaylistID: "playlist_defaultsquad", Region: "EU", TeamFill: true}
	now := time.Date(2026, 2, 9, 12, 0, 0, 0, time.UTC)

	snapshot := &Party{
		ID:       "p1",
		Revision: 4,
		Meta: map[string]string{
			"Default:RegionId_s": "EU",
		},
		Members: map[string]Member{
			"acc1": {
				AccountID: "acc1",
				Meta: map[string]string{
					"Default:LobbyState_j": `{"LobbyState":{"gameReadiness":"NotReady"}}`,
				},
			},
		},
	}

	left, err := builder.BuildIntents(snapshot, PatchIntent{Ready: &ready, Playlist: &playlist}, now)
	if err != nil {
		t.Fatalf("build intents left: %v", err)
	}

	right, err := builder.BuildIntents(snapshot, PatchIntent{Ready: &ready, Playlist: &playlist}, now)
	if err != nil {
		t.Fatalf("build intents right: %v", err)
	}

	if !reflect.DeepEqual(buildMetaPatch(left.PartyMetaUpdates), buildMetaPatch(right.PartyMetaUpdates)) {
		t.Fatalf("expected deterministic party patch")
	}

	if !reflect.DeepEqual(buildMetaPatch(left.MemberMetaUpdates), buildMetaPatch(right.MemberMetaUpdates)) {
		t.Fatalf("expected deterministic member patch")
	}
}

func TestPatchBuilderSetOutfitAndEmote(t *testing.T) {
	builder := NewIntentPatchBuilder("acc1")
	outfit := "CID_A_123"
	emote := "EID_Wave"

	snapshot := &Party{
		ID:       "p1",
		Revision: 4,
		Meta:     map[string]string{},
		Members: map[string]Member{
			"acc1": {
				AccountID: "acc1",
				Meta:      map[string]string{},
			},
		},
	}

	built, err := builder.BuildIntents(snapshot, PatchIntent{
		OutfitID: &outfit,
		EmoteID:  &emote,
	}, time.Date(2026, 2, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("build intents: %v", err)
	}

	cosmetic, ok := built.MemberMetaUpdates["Default:AthenaCosmeticLoadout_j"].(map[string]any)
	if !ok {
		t.Fatalf("expected cosmetic update")
	}

	cosmeticRoot := decodeJSONMapValue(cosmetic, "AthenaCosmeticLoadout")
	if got := cosmeticRoot["characterPrimaryAssetId"]; got != "AthenaCharacter:CID_A_123" {
		t.Fatalf("unexpected characterPrimaryAssetId: %v", got)
	}

	mpLoadout, ok := built.MemberMetaUpdates["Default:MpLoadout_j"].(map[string]any)
	if !ok {
		t.Fatalf("expected mp loadout update")
	}

	mpRoot := decodeJSONMapValue(mpLoadout, "MpLoadout")
	mpData := asJSONMap(mpRoot["d"])
	ac := decodeJSONMapValue(mpData, "ac")
	if got := ac["i"]; got != "CID_A_123" {
		t.Fatalf("unexpected mp loadout outfit id: %v", got)
	}

	emotePayload, ok := built.MemberMetaUpdates["Default:FrontendEmote_j"].(map[string]any)
	if !ok {
		t.Fatalf("expected emote update")
	}

	emoteRoot := decodeJSONMapValue(emotePayload, "FrontendEmote")
	if got := emoteRoot["pickable"]; got != "/BRCosmetics/Athena/Items/Cosmetics/Dances/EID_Wave.EID_Wave" {
		t.Fatalf("unexpected emote path: %v", got)
	}

	if got := emoteRoot["emoteSection"]; got != -2 {
		t.Fatalf("unexpected emote section: %v", got)
	}
}

func TestPatchBuilderClearEmote(t *testing.T) {
	builder := NewIntentPatchBuilder("acc1")
	snapshot := &Party{
		ID:       "p1",
		Revision: 5,
		Meta:     map[string]string{},
		Members: map[string]Member{
			"acc1": {
				AccountID: "acc1",
				Meta: map[string]string{
					"Default:FrontendEmote_j": `{"FrontendEmote":{"pickable":"/BRCosmetics/Athena/Items/Cosmetics/Dances/EID_Wave.EID_Wave","emoteSection":-2}}`,
				},
			},
		},
	}

	built, err := builder.BuildIntents(snapshot, PatchIntent{ClearEmote: true}, time.Date(2026, 2, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("build intents: %v", err)
	}

	emotePayload, ok := built.MemberMetaUpdates["Default:FrontendEmote_j"].(map[string]any)
	if !ok {
		t.Fatalf("expected emote update")
	}

	emoteRoot := decodeJSONMapValue(emotePayload, "FrontendEmote")
	if got := emoteRoot["pickable"]; got != "None" {
		t.Fatalf("unexpected clear emote pickable: %v", got)
	}

	if got := emoteRoot["emoteSection"]; got != -1 {
		t.Fatalf("unexpected clear emote section: %v", got)
	}
}
