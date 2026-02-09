package presence

import "time"

type Status string

const (
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
	StatusAway    Status = "away"
)

type FriendPresence struct {
	AccountID string
	Status    Status
	UpdatedAt time.Time
}
