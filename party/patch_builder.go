package party

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ceskypane/fngo/matchmaking"
	partytemplates "github.com/ceskypane/fngo/party/templates"
)

type PatchIntent struct {
	Ready      *ReadyState
	Playlist   *matchmaking.PlaylistRequest
	CustomKey  *string
	Loadout    *Loadout
	OutfitID   *string
	EmoteID    *string
	ClearEmote bool
}

type BuiltPatch struct {
	PartyMetaUpdates  map[string]any
	MemberMetaUpdates map[string]any
}

func NewIntentPatchBuilder(accountID string) *PatchBuilder {
	return &PatchBuilder{
		accountID:      accountID,
		partyTemplate:  partytemplates.DefaultPartyMeta(),
		memberTemplate: partytemplates.DefaultPartyMemberMeta(),
	}
}

func (b *PatchBuilder) BuildIntents(snapshot *Party, intent PatchIntent, at time.Time) (BuiltPatch, error) {
	if snapshot == nil {
		return BuiltPatch{}, fmt.Errorf("party: snapshot is required")
	}

	member := snapshot.Members[b.accountID]
	partyCurrent := decodeMetaSchema(snapshot.Meta)
	memberCurrent := decodeMetaSchema(member.Meta)

	partyBaseline := b.copyPartyTemplate()
	memberBaseline := b.copyMemberTemplate()

	partytemplates.DeepMerge(partyBaseline, partyCurrent)
	partytemplates.DeepMerge(memberBaseline, memberCurrent)

	touchedParty := map[string]struct{}{}
	touchedMember := map[string]struct{}{}

	if intent.CustomKey != nil {
		partyBaseline["Default:CustomMatchKey_s"] = strings.TrimSpace(*intent.CustomKey)
		touchedParty["Default:CustomMatchKey_s"] = struct{}{}
	}

	if intent.Ready != nil {
		lobby := asJSONMap(memberBaseline["Default:LobbyState_j"])
		lobbyState := asJSONMap(lobby["LobbyState"])
		lobbyState["gameReadiness"] = string(*intent.Ready)

		if *intent.Ready == ReadyStateReady {
			lobbyState["readyInputType"] = "MouseAndKeyboard"
		} else {
			lobbyState["readyInputType"] = "Count"
		}

		lobby["LobbyState"] = lobbyState
		memberBaseline["Default:LobbyState_j"] = lobby
		touchedMember["Default:LobbyState_j"] = struct{}{}
	}

	if intent.Playlist != nil {
		req := *intent.Playlist
		ts := at.UTC().Unix()
		islandPayload := map[string]any{"LinkId": req.PlaylistID}
		islandRaw, _ := json.Marshal(islandPayload)

		matchInfo := asJSONMap(memberBaseline["Default:MatchmakingInfo_j"])
		mm := asJSONMap(matchInfo["MatchmakingInfo"])
		mm["islandSelection"] = map[string]any{
			"island":    string(islandRaw),
			"timestamp": ts,
		}
		mm["currentIsland"] = map[string]any{
			"island":    string(islandRaw),
			"timestamp": ts,
		}
		matchInfo["MatchmakingInfo"] = mm
		memberBaseline["Default:MatchmakingInfo_j"] = matchInfo
		touchedMember["Default:MatchmakingInfo_j"] = struct{}{}

		partyBaseline["Default:AthenaSquadFill_b"] = req.TeamFill
		touchedParty["Default:AthenaSquadFill_b"] = struct{}{}

		if req.Region != "" {
			partyBaseline["Default:RegionId_s"] = req.Region
			touchedParty["Default:RegionId_s"] = struct{}{}
		}

		selectedIsland := asJSONMap(partyBaseline["Default:SelectedIsland_j"])
		selectedRoot := asJSONMap(selectedIsland["SelectedIsland"])
		selectedRoot["linkId"] = map[string]any{
			"mnemonic": strings.ToLower(req.PlaylistID),
			"version":  -1,
		}
		selectedIsland["SelectedIsland"] = selectedRoot
		partyBaseline["Default:SelectedIsland_j"] = selectedIsland
		touchedParty["Default:SelectedIsland_j"] = struct{}{}
	}

	if intent.Loadout != nil {
		loadout := *intent.Loadout
		cosmetic := asJSONMap(memberBaseline["Default:AthenaCosmeticLoadout_j"])
		root := asJSONMap(cosmetic["AthenaCosmeticLoadout"])

		if loadout.Character != "" {
			root["characterPrimaryAssetId"] = loadout.Character
		}

		if loadout.Backpack != "" {
			root["backpackDef"] = loadout.Backpack
		}

		if loadout.Pickaxe != "" {
			root["pickaxeDef"] = loadout.Pickaxe
		}

		cosmetic["AthenaCosmeticLoadout"] = root
		memberBaseline["Default:AthenaCosmeticLoadout_j"] = cosmetic
		touchedMember["Default:AthenaCosmeticLoadout_j"] = struct{}{}

		if loadout.Emote != "" {
			emote := asJSONMap(memberBaseline["Default:FrontendEmote_j"])
			emoteRoot := asJSONMap(emote["FrontendEmote"])
			emoteRoot["pickable"] = loadout.Emote
			emote["FrontendEmote"] = emoteRoot
			memberBaseline["Default:FrontendEmote_j"] = emote
			touchedMember["Default:FrontendEmote_j"] = struct{}{}
		}
	}

	if intent.OutfitID != nil {
		outfitID := normalizeOutfitID(*intent.OutfitID)
		if outfitID != "" {
			cosmetic := asJSONMap(memberBaseline["Default:AthenaCosmeticLoadout_j"])
			root := asJSONMap(cosmetic["AthenaCosmeticLoadout"])
			root["characterPrimaryAssetId"] = "AthenaCharacter:" + outfitID
			cosmetic["AthenaCosmeticLoadout"] = root
			memberBaseline["Default:AthenaCosmeticLoadout_j"] = cosmetic
			touchedMember["Default:AthenaCosmeticLoadout_j"] = struct{}{}

			mpLoadout := asJSONMap(memberBaseline["Default:MpLoadout_j"])
			mp := asJSONMap(mpLoadout["MpLoadout"])
			mpData := asJSONMap(mp["d"])
			ac := asJSONMap(mpData["ac"])
			ac["i"] = outfitID
			mpData["ac"] = ac

			mpRaw, _ := json.Marshal(mpData)
			mp["d"] = string(mpRaw)
			mpLoadout["MpLoadout"] = mp
			memberBaseline["Default:MpLoadout_j"] = mpLoadout
			touchedMember["Default:MpLoadout_j"] = struct{}{}
		}
	}

	if intent.ClearEmote {
		emote := asJSONMap(memberBaseline["Default:FrontendEmote_j"])
		emoteRoot := asJSONMap(emote["FrontendEmote"])
		emoteRoot["pickable"] = "None"
		emoteRoot["emoteSection"] = -1
		emote["FrontendEmote"] = emoteRoot
		memberBaseline["Default:FrontendEmote_j"] = emote
		touchedMember["Default:FrontendEmote_j"] = struct{}{}
	}

	if intent.EmoteID != nil {
		emoteID := strings.TrimSpace(*intent.EmoteID)
		if emoteID != "" {
			emote := asJSONMap(memberBaseline["Default:FrontendEmote_j"])
			emoteRoot := asJSONMap(emote["FrontendEmote"])
			emoteRoot["pickable"] = buildEmotePath(emoteID)
			emoteRoot["emoteSection"] = -2
			emote["FrontendEmote"] = emoteRoot
			memberBaseline["Default:FrontendEmote_j"] = emote
			touchedMember["Default:FrontendEmote_j"] = struct{}{}
		}
	}

	partyUpdates := make(map[string]any)
	for key := range touchedParty {
		value, ok := partyBaseline[key]
		if !ok {
			continue
		}

		serialized := serializeMetaValue(key, value)
		if snapshot.Meta[key] == serialized {
			continue
		}

		partyUpdates[key] = value
	}

	memberUpdates := make(map[string]any)
	for key := range touchedMember {
		value, ok := memberBaseline[key]
		if !ok {
			continue
		}

		serialized := serializeMetaValue(key, value)
		if member.Meta[key] == serialized {
			continue
		}

		memberUpdates[key] = value
	}

	return BuiltPatch{
		PartyMetaUpdates:  partyUpdates,
		MemberMetaUpdates: memberUpdates,
	}, nil
}

func (b *PatchBuilder) copyPartyTemplate() map[string]any {
	if len(b.partyTemplate) == 0 {
		return partytemplates.DefaultPartyMeta()
	}

	return partytemplates.DeepMerge(map[string]any{}, b.partyTemplate)
}

func (b *PatchBuilder) copyMemberTemplate() map[string]any {
	if len(b.memberTemplate) == 0 {
		return partytemplates.DefaultPartyMemberMeta()
	}

	return partytemplates.DeepMerge(map[string]any{}, b.memberTemplate)
}

func asJSONMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case string:
		return decodeJSONMap(typed)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return map[string]any{}
		}

		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return map[string]any{}
		}

		if parsed == nil {
			return map[string]any{}
		}

		return parsed
	}
}

func normalizeOutfitID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "AthenaCharacter:")
	return strings.TrimSpace(id)
}

func buildEmotePath(emoteID string) string {
	emoteID = strings.TrimSpace(emoteID)
	if emoteID == "" {
		return "None"
	}

	if strings.Contains(emoteID, "/") && strings.Contains(emoteID, ".") {
		return emoteID
	}

	return "/BRCosmetics/Athena/Items/Cosmetics/Dances/" + emoteID + "." + emoteID
}
