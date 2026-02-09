package events

import "strings"

func IsName(name Name) Predicate {
	return func(evt Event) bool {
		if evt == nil {
			return false
		}

		return evt.Name() == name
	}
}

func PartyUpdatedAny() Predicate {
	return func(evt Event) bool {
		_, ok := evt.(PartyUpdated)
		return ok
	}
}

func PartyUpdatedForID(partyID string) Predicate {
	return func(evt Event) bool {
		update, ok := evt.(PartyUpdated)
		if !ok {
			return false
		}

		return update.PartyID == partyID
	}
}

func PartyPlaylistEquals(partyID, playlist string) Predicate {
	normalizedPlaylist := strings.ToLower(strings.TrimSpace(playlist))

	return func(evt Event) bool {
		update, ok := evt.(PartyUpdated)
		if !ok {
			return false
		}

		if partyID != "" && update.PartyID != partyID {
			return false
		}

		return strings.ToLower(strings.TrimSpace(update.Playlist)) == normalizedPlaylist
	}
}

func PartyCustomKeyEquals(partyID, key string) Predicate {
	return func(evt Event) bool {
		update, ok := evt.(PartyUpdated)
		if !ok {
			return false
		}

		if partyID != "" && update.PartyID != partyID {
			return false
		}

		return update.CustomKey == key
	}
}

func PartyMemberUpdatedFor(memberID string) Predicate {
	return func(evt Event) bool {
		update, ok := evt.(PartyMemberUpdated)
		if !ok {
			return false
		}

		if memberID == "" {
			return true
		}

		return update.MemberID == memberID
	}
}

func PartyCaptainIs(partyID, captainID string) Predicate {
	return func(evt Event) bool {
		change, ok := evt.(PartyCaptainChanged)
		if !ok {
			return false
		}

		if partyID != "" && change.PartyID != partyID {
			return false
		}

		return change.CaptainID == captainID
	}
}

func Any(predicates ...Predicate) Predicate {
	return func(evt Event) bool {
		for _, predicate := range predicates {
			if predicate == nil {
				continue
			}

			if predicate(evt) {
				return true
			}
		}

		return false
	}
}
