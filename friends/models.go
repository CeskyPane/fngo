package friends

import (
	"context"
	"fmt"
	"time"
)

type Friend struct {
	AccountID   string
	DisplayName string
	CreatedAt   *time.Time
}

type FriendRequest struct {
	AccountID   string
	DisplayName string
	CreatedAt   *time.Time
}

type BulkOperationError struct {
	AccountID string
	Err       error
}

func (e BulkOperationError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("friends bulk op failed for account=%s", e.AccountID)
	}
	return fmt.Sprintf("friends bulk op failed for account=%s: %v", e.AccountID, e.Err)
}

func (e BulkOperationError) Unwrap() error {
	return e.Err
}

type BulkRemoveResult struct {
	Attempted int
	Removed   int
	Failed    []BulkOperationError
}

type FriendsLister interface {
	ListFriends(ctx context.Context) ([]Friend, error)
}
