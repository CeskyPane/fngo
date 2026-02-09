package fngo

import (
	"context"

	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/matchmaking"
	"github.com/ceskypane/fngo/party"
)

// BotClient is the runtime-facing control surface for bot orchestration layers.
type BotClient interface {
	Login(ctx context.Context) error
	Logout(ctx context.Context) error

	EnsureParty(ctx context.Context) error
	JoinParty(ctx context.Context, id string) error
	JoinPartyByMemberID(ctx context.Context, memberAccountID string) error
	LeaveParty(ctx context.Context) error
	PromoteMember(ctx context.Context, accountID string) error
	SetReady(ctx context.Context, state party.ReadyState) error
	SetPlaylist(ctx context.Context, req matchmaking.PlaylistRequest) error
	SetCustomKey(ctx context.Context, key string) error
	SetOutfit(ctx context.Context, outfitID string) error
	SetEmote(ctx context.Context, emoteID string) error
	ClearEmote(ctx context.Context) error
	SetLoadout(ctx context.Context, loadout party.Loadout) error

	Events() <-chan events.Event
	WaitFor(ctx context.Context, predicate events.Predicate) (events.Event, error)
}
