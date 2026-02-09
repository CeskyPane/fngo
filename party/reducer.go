package party

import (
	"errors"
	"sync"
	"time"
)

var ErrUnknownEvent = errors.New("party: unknown reducer event")

type State struct {
	mu    sync.RWMutex
	party *Party
}

func NewState() *State {
	return &State{}
}

func (s *State) Snapshot() *Party {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.party == nil {
		return nil
	}

	cp := *s.party
	cp.Members = make(map[string]Member, len(s.party.Members))
	for key, value := range s.party.Members {
		memberCopy := value
		memberCopy.Meta = cloneStringMap(value.Meta)
		cp.Members[key] = memberCopy
	}
	cp.Meta = cloneStringMap(s.party.Meta)

	return &cp
}

func (s *State) Replace(p Party) {
	s.mu.Lock()
	defer s.mu.Unlock()

	partyCopy := p
	partyCopy.Members = make(map[string]Member, len(p.Members))
	for key, value := range p.Members {
		memberCopy := value
		memberCopy.Meta = cloneStringMap(value.Meta)
		partyCopy.Members[key] = memberCopy
	}
	partyCopy.Meta = cloneStringMap(p.Meta)

	s.party = &partyCopy
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}

	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}

	return out
}

func (s *State) ApplyEvent(e ReducerEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureParty(e.PartyID)
	s.bumpRevision(e.Revision)
	s.touch(e.At)

	s.ensureMaps()

	s.applyPartyMeta(e.PartyMetaUpdates, e.PartyMetaRemoved)

	syncFromMeta := func() {
		if value, ok := s.party.Meta["Default:CustomMatchKey_s"]; ok {
			s.party.CustomKey = value
		}
		if value, ok := s.party.Meta["Default:RegionId_s"]; ok {
			s.party.RegionID = value
		}
		if value, ok := s.party.Meta["Default:SelectedIsland_j"]; ok {
			if playlist := extractPlaylist(value); playlist != "" {
				s.party.Playlist = playlist
			}
		}
	}

	switch e.Kind {
	case EventPartySnapshot:
		s.party.ID = e.PartyID
		s.party.Revision = e.Revision

		if e.CaptainID != "" {
			s.party.CaptainID = e.CaptainID
		}

		if e.Playlist != "" {
			s.party.Playlist = e.Playlist
		}

		if e.CustomKey != "" {
			s.party.CustomKey = e.CustomKey
		}

		syncFromMeta()
		return nil
	case EventMemberJoined:
		if e.Member.AccountID == "" {
			return nil
		}

		member := e.Member
		if member.JoinedAt.IsZero() {
			member.JoinedAt = s.party.UpdatedAt
		}

		if member.Meta == nil {
			member.Meta = map[string]string{}
		}

		s.party.Members[member.AccountID] = member

		if member.Role == "CAPTAIN" {
			s.party.CaptainID = member.AccountID
		}

		if s.party.CaptainID == "" {
			s.party.CaptainID = member.AccountID
		}

		return nil
	case EventMemberUpdated:
		if e.Member.AccountID == "" {
			return nil
		}

		existing := s.party.Members[e.Member.AccountID]
		if existing.AccountID == "" {
			existing.AccountID = e.Member.AccountID
		}

		if e.Member.DisplayName != "" {
			existing.DisplayName = e.Member.DisplayName
		}

		if e.Member.Role != "" {
			existing.Role = e.Member.Role
			if e.Member.Role == "CAPTAIN" {
				s.party.CaptainID = e.Member.AccountID
			}
		}

		if e.Member.Ready != "" {
			existing.Ready = e.Member.Ready
		}

		if !isLoadoutZero(e.Member.Loadout) {
			existing.Loadout = mergeLoadout(existing.Loadout, e.Member.Loadout)
		}

		if e.Member.Revision > existing.Revision {
			existing.Revision = e.Member.Revision
		}

		if !e.Member.JoinedAt.IsZero() {
			existing.JoinedAt = e.Member.JoinedAt
		}

		if existing.Meta == nil {
			existing.Meta = map[string]string{}
		}

		for key, value := range e.Member.Meta {
			existing.Meta[key] = value
		}

		for _, key := range e.MemberMetaRemoved {
			delete(existing.Meta, key)
		}

		s.party.Members[e.Member.AccountID] = existing
		return nil
	case EventMemberLeft, EventMemberKicked:
		if e.MemberID == "" {
			return nil
		}

		delete(s.party.Members, e.MemberID)
		if s.party.CaptainID == e.MemberID {
			s.party.CaptainID = ""
		}

		return nil
	case EventPartyUpdated:
		if e.CaptainID != "" {
			s.party.CaptainID = e.CaptainID
		}

		if e.Playlist != "" {
			s.party.Playlist = e.Playlist
		}

		if e.CustomKey != "" {
			s.party.CustomKey = e.CustomKey
		}

		syncFromMeta()
		return nil
	default:
		return ErrUnknownEvent
	}
}

func (s *State) ensureParty(id string) {
	if s.party != nil {
		if s.party.ID == "" && id != "" {
			s.party.ID = id
		}

		return
	}

	s.party = &Party{
		ID:      id,
		Members: make(map[string]Member),
		Meta:    make(map[string]string),
	}
}

func (s *State) ensureMaps() {
	if s.party == nil {
		return
	}

	if s.party.Members == nil {
		s.party.Members = make(map[string]Member)
	}

	if s.party.Meta == nil {
		s.party.Meta = make(map[string]string)
	}
}

func (s *State) bumpRevision(revision int64) {
	if s.party == nil {
		return
	}

	if revision > s.party.Revision {
		s.party.Revision = revision
	}
}

func (s *State) touch(at time.Time) {
	if s.party == nil {
		return
	}

	if at.IsZero() {
		at = time.Now().UTC()
	}

	s.party.UpdatedAt = at
}

func (s *State) applyPartyMeta(updates map[string]string, removed []string) {
	if s.party == nil {
		return
	}

	if s.party.Meta == nil {
		s.party.Meta = make(map[string]string)
	}

	for key, value := range updates {
		s.party.Meta[key] = value
	}

	for _, key := range removed {
		delete(s.party.Meta, key)
	}
}

func isLoadoutZero(loadout Loadout) bool {
	return loadout.Character == "" &&
		loadout.Backpack == "" &&
		loadout.Pickaxe == "" &&
		loadout.Emote == ""
}

func mergeLoadout(current, updates Loadout) Loadout {
	out := current
	if updates.Character != "" {
		out.Character = updates.Character
	}

	if updates.Backpack != "" {
		out.Backpack = updates.Backpack
	}

	if updates.Pickaxe != "" {
		out.Pickaxe = updates.Pickaxe
	}

	if updates.Emote != "" {
		out.Emote = updates.Emote
	}

	return out
}
