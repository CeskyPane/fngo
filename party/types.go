package party

import (
	"context"
	"time"

	"github.com/ceskypane/fngo/logging"
	"github.com/ceskypane/fngo/matchmaking"
	transporthttp "github.com/ceskypane/fngo/transport/http"
)

type ReadyState string

const (
	ReadyStateNotReady ReadyState = "NotReady"
	ReadyStateReady    ReadyState = "Ready"
)

type Loadout struct {
	Character string
	Backpack  string
	Pickaxe   string
	Emote     string
}

type Member struct {
	AccountID   string
	DisplayName string
	Role        string
	Ready       ReadyState
	Loadout     Loadout
	JoinedAt    time.Time
	Revision    int64
	Meta        map[string]string
}

type Party struct {
	ID        string
	Revision  int64
	CaptainID string
	Playlist  string
	CustomKey string
	RegionID  string
	Members   map[string]Member
	Meta      map[string]string
	UpdatedAt time.Time
}

type MetadataMutation struct {
	Key   string
	Value any
}

type Patch struct {
	BaseRevision int64
	Mutations    []MetadataMutation
}

type PatchBuilder struct {
	patch Patch

	accountID      string
	partyTemplate  map[string]any
	memberTemplate map[string]any
}

func NewPatchBuilder(baseRevision int64) *PatchBuilder {
	return &PatchBuilder{
		patch: Patch{
			BaseRevision: baseRevision,
			Mutations:    make([]MetadataMutation, 0, 4),
		},
	}
}

func (b *PatchBuilder) Set(key string, value any) *PatchBuilder {
	b.patch.Mutations = append(b.patch.Mutations, MetadataMutation{Key: key, Value: value})

	return b
}

func (b *PatchBuilder) Build() Patch {
	out := Patch{
		BaseRevision: b.patch.BaseRevision,
		Mutations:    make([]MetadataMutation, len(b.patch.Mutations)),
	}
	copy(out.Mutations, b.patch.Mutations)

	return out
}

type EventKind string

const (
	EventPartySnapshot EventKind = "party.snapshot"
	EventMemberJoined  EventKind = "party.member_joined"
	EventMemberLeft    EventKind = "party.member_left"
	EventMemberUpdated EventKind = "party.member_updated"
	EventMemberKicked  EventKind = "party.member_kicked"
	EventPartyUpdated  EventKind = "party.updated"
)

type ReducerEvent struct {
	Kind     EventKind
	PartyID  string
	Revision int64

	Member   Member
	MemberID string

	CaptainID string
	Playlist  string
	CustomKey string

	PartyMetaUpdates map[string]string
	PartyMetaRemoved []string

	MemberMetaRemoved []string

	At time.Time
}

type HTTPClient interface {
	Request(ctx context.Context, req transporthttp.Request) (transporthttp.Response, error)
}

type Config struct {
	BaseURL      string
	AccountID    string
	DisplayName  string
	Platform     string
	BuildID      string
	ConnectionID string

	MaxPatchRetries int
	MinPatchBackoff time.Duration
	MaxPatchBackoff time.Duration

	DisableAutoCreateAfterLeave bool
	Logger                      logging.Logger
}

func DefaultConfig() Config {
	return Config{
		BaseURL:         "https://party-service-prod.ol.epicgames.com/party/api/v1/Fortnite",
		Platform:        "WIN",
		BuildID:         "1:3:",
		MaxPatchRetries: 3,
		MinPatchBackoff: 100 * time.Millisecond,
		MaxPatchBackoff: 500 * time.Millisecond,
	}
}

type Commander interface {
	SyncCurrentParty(ctx context.Context) error
	EnsureParty(ctx context.Context) (*Party, error)
	CreateParty(ctx context.Context) (*Party, error)
	JoinParty(ctx context.Context, id string) error
	JoinPartyByMemberID(ctx context.Context, memberAccountID string) error
	LeaveParty(ctx context.Context) error
	PromoteMember(ctx context.Context, partyID string, accountID string) error
	SetReady(ctx context.Context, state ReadyState) error
	SetPlaylist(ctx context.Context, req matchmaking.PlaylistRequest) error
	SetCustomKey(ctx context.Context, key string) error
	SetOutfit(ctx context.Context, outfitID string) error
	SetEmote(ctx context.Context, emoteID string) error
	ClearEmote(ctx context.Context) error
	SetLoadout(ctx context.Context, loadout Loadout) error
	ResyncAfterReconnect(ctx context.Context) error
}
