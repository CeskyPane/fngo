package events

import "time"

type Name string

const (
	EventClientReady        Name = "client.ready"
	EventClientDisconnected Name = "client.disconnected"

	EventAuthRefreshed     Name = "auth.refreshed"
	EventAuthRefreshFailed Name = "auth.refresh_failed"
	EventAuthFatal         Name = "auth.fatal"

	EventXMPPConnected    Name = "xmpp.connected"
	EventXMPPDisconnected Name = "xmpp.disconnected"
	EventXMPPReconnecting Name = "xmpp.reconnecting"
	EventXMPPError        Name = "xmpp.error"
	EventXMPPFrame        Name = "xmpp.frame"
	EventXMPPMessage      Name = "xmpp.message"
	EventXMPPPresence     Name = "xmpp.presence"
	EventXMPPIQ           Name = "xmpp.iq"
	EventXMPPRaw          Name = "xmpp.raw"

	EventPresenceUpdated Name = "presence.updated"

	EventPartyUpdated             Name = "party.updated"
	EventPartyMemberJoined        Name = "party.member_joined"
	EventPartyMemberLeft          Name = "party.member_left"
	EventPartyMemberUpdated       Name = "party.member_updated"
	EventPartyMemberKicked        Name = "party.member_kicked"
	EventPartyCaptainChanged      Name = "party.captain_changed"
	EventPartyInviteReceived      Name = "party.invite_received"
	EventPartyJoinRequestReceived Name = "party.join_request_received"
	EventPartyRawNotification     Name = "party.raw_notification"

	EventFriendRequestReceived Name = "friend.request_received"
	EventFriendAdded           Name = "friend.added"
	EventFriendRemoved         Name = "friend.removed"
	EventFriendRequestDeclined Name = "friend.request_declined"
)

type Event interface {
	Name() Name
	Timestamp() time.Time
}

type Base struct {
	At time.Time
}

func (b Base) Timestamp() time.Time {
	return b.At
}

type ClientReady struct {
	Base
}

func (e ClientReady) Name() Name {
	return EventClientReady
}

type ClientDisconnected struct {
	Base
	Err error
}

func (e ClientDisconnected) Name() Name {
	return EventClientDisconnected
}

type AuthRefreshed struct {
	Base
	ExpiresAt time.Time
}

func (e AuthRefreshed) Name() Name {
	return EventAuthRefreshed
}

type AuthRefreshFailed struct {
	Base
	Err error
}

func (e AuthRefreshFailed) Name() Name {
	return EventAuthRefreshFailed
}

type AuthFatal struct {
	Base
	Err error
}

func (e AuthFatal) Name() Name {
	return EventAuthFatal
}

type XMPPConnected struct {
	Base
	Endpoint string
}

func (e XMPPConnected) Name() Name {
	return EventXMPPConnected
}

type XMPPDisconnected struct {
	Base
	Err error
}

func (e XMPPDisconnected) Name() Name {
	return EventXMPPDisconnected
}

type XMPPReconnecting struct {
	Base
	Attempt int
	Delay   time.Duration
	Err     error
}

func (e XMPPReconnecting) Name() Name {
	return EventXMPPReconnecting
}

type XMPPError struct {
	Base
	Err   error
	Fatal bool
}

func (e XMPPError) Name() Name {
	return EventXMPPError
}

type XMPPFrame struct {
	Base
	MessageType int
	Payload     string
}

func (e XMPPFrame) Name() Name {
	return EventXMPPFrame
}

type XMPPMessage struct {
	Base
	ID   string
	From string
	To   string
	Type string
	Body string
	Raw  string
}

func (e XMPPMessage) Name() Name {
	return EventXMPPMessage
}

type XMPPPresence struct {
	Base
	ID   string
	From string
	To   string
	Type string
	Raw  string
}

func (e XMPPPresence) Name() Name {
	return EventXMPPPresence
}

type XMPPIQ struct {
	Base
	ID   string
	From string
	To   string
	Type string
	Raw  string
}

func (e XMPPIQ) Name() Name {
	return EventXMPPIQ
}

type XMPPRaw struct {
	Base
	Raw string
	Err error
}

func (e XMPPRaw) Name() Name {
	return EventXMPPRaw
}

type PresenceUpdated struct {
	Base
	AccountID  string
	PartyID    string
	IsPrivate  bool
	IsJoinable bool
	Raw        string
}

func (e PresenceUpdated) Name() Name {
	return EventPresenceUpdated
}

type PartyUpdated struct {
	Base
	PartyID   string
	Revision  int64
	CaptainID string
	Playlist  string
	CustomKey string
}

func (e PartyUpdated) Name() Name {
	return EventPartyUpdated
}

type PartyMemberJoined struct {
	Base
	PartyID     string
	MemberID    string
	DisplayName string
	Revision    int64
}

func (e PartyMemberJoined) Name() Name {
	return EventPartyMemberJoined
}

type PartyMemberLeft struct {
	Base
	PartyID  string
	MemberID string
	Revision int64
	Reason   string
}

func (e PartyMemberLeft) Name() Name {
	return EventPartyMemberLeft
}

type PartyMemberUpdated struct {
	Base
	PartyID         string
	MemberID        string
	Revision        int64
	UpdatedMetaKeys []string
}

func (e PartyMemberUpdated) Name() Name {
	return EventPartyMemberUpdated
}

type PartyMemberKicked struct {
	Base
	PartyID  string
	MemberID string
	ActorID  string
	Revision int64
}

type FriendRequestReceived struct {
	Base
	AccountID string
}

func (e FriendRequestReceived) Name() Name {
	return EventFriendRequestReceived
}

type FriendAdded struct {
	Base
	AccountID string
}

func (e FriendAdded) Name() Name {
	return EventFriendAdded
}

type FriendRemoved struct {
	Base
	AccountID string
	Reason    string
}

func (e FriendRemoved) Name() Name {
	return EventFriendRemoved
}

type FriendRequestDeclined struct {
	Base
	AccountID string
}

func (e FriendRequestDeclined) Name() Name {
	return EventFriendRequestDeclined
}

func (e PartyMemberKicked) Name() Name {
	return EventPartyMemberKicked
}

type PartyCaptainChanged struct {
	Base
	PartyID   string
	CaptainID string
	Revision  int64
}

func (e PartyCaptainChanged) Name() Name {
	return EventPartyCaptainChanged
}

type PartyInviteReceived struct {
	Base
	PartyID  string
	SenderID string
	Revision int64
}

func (e PartyInviteReceived) Name() Name {
	return EventPartyInviteReceived
}

type PartyJoinRequestReceived struct {
	Base
	PartyID     string
	RequesterID string
	Revision    int64
}

func (e PartyJoinRequestReceived) Name() Name {
	return EventPartyJoinRequestReceived
}

type PartyRawNotification struct {
	Base
	NotificationType string
	PartyID          string
	Payload          map[string]any
	Raw              string
}

func (e PartyRawNotification) Name() Name {
	return EventPartyRawNotification
}
