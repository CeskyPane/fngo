package friends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ceskypane/fngo/logging"
	transporthttp "github.com/ceskypane/fngo/transport/http"
)

var (
	ErrNotConfigured = errors.New("friends: commands not configured")
)

type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e == nil {
		return "friends: api error"
	}

	if e.Code != "" {
		return fmt.Sprintf("friends api error status=%d code=%s message=%s", e.StatusCode, e.Code, e.Message)
	}

	return fmt.Sprintf("friends api error status=%d message=%s", e.StatusCode, e.Message)
}

type HTTPClient interface {
	Request(ctx context.Context, req transporthttp.Request) (transporthttp.Response, error)
}

type Commands struct {
	http HTTPClient
	cfg  Config

	now func() time.Time
	log logging.Logger
}

type summaryEntry struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Created     string `json:"created"`
}

type summaryResponse struct {
	Friends  []summaryEntry `json:"friends"`
	Incoming []summaryEntry `json:"incoming"`
	Outgoing []summaryEntry `json:"outgoing"`
}

func NewCommands(httpClient HTTPClient, cfg Config) *Commands {
	defaults := DefaultConfig()
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaults.BaseURL
	}

	return &Commands{
		http: httpClient,
		cfg:  cfg,
		now:  time.Now,
		log:  logging.With(cfg.Logger),
	}
}

func (c *Commands) SetIdentity(accountID string) {
	accountID = trimSpace(accountID)
	if accountID == "" {
		return
	}

	c.cfg.AccountID = accountID
}

// AddFriend sends or accepts a friendship request.
// Epic endpoint: POST /friends/api/public/friends/{selfAccountId}/{targetAccountId}
func (c *Commands) AddFriend(ctx context.Context, targetAccountID string) error {
	if err := c.validateConfig(); err != nil {
		return err
	}

	targetAccountID = trimSpace(targetAccountID)
	if targetAccountID == "" {
		return fmt.Errorf("friends: target account id is required")
	}

	resp, err := c.requestNoBody(ctx, http.MethodPost, fmt.Sprintf("/friends/api/public/friends/%s/%s", c.cfg.AccountID, targetAccountID))
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		return decodeAPIError(resp)
	}

	return nil
}

// RemoveFriend removes a friend, aborts an outgoing request, or declines an incoming request.
// Epic endpoint: DELETE /friends/api/v1/{selfAccountId}/friends/{targetAccountId}
func (c *Commands) RemoveFriend(ctx context.Context, targetAccountID string) error {
	if err := c.validateConfig(); err != nil {
		return err
	}

	targetAccountID = trimSpace(targetAccountID)
	if targetAccountID == "" {
		return fmt.Errorf("friends: target account id is required")
	}

	resp, err := c.requestNoBody(ctx, http.MethodDelete, fmt.Sprintf("/friends/api/v1/%s/friends/%s", c.cfg.AccountID, targetAccountID))
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		return decodeAPIError(resp)
	}

	return nil
}

// ListFriends returns the friend list from Epic summary endpoint.
// Endpoint (fnbr.js mapping): GET /friends/api/v1/{selfAccountId}/summary
func (c *Commands) ListFriends(ctx context.Context) ([]Friend, error) {
	summary, err := c.fetchSummary(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Friend, 0, len(summary.Friends))
	for _, item := range summary.Friends {
		out = append(out, Friend{
			AccountID:   trimSpace(item.AccountID),
			DisplayName: trimSpace(item.DisplayName),
			CreatedAt:   parseCreatedAt(item.Created),
		})
	}
	return out, nil
}

// ListIncomingFriendRequests returns incoming pending friend requests from summary endpoint.
// Endpoint (fnbr.js mapping): GET /friends/api/v1/{selfAccountId}/summary
func (c *Commands) ListIncomingFriendRequests(ctx context.Context) ([]FriendRequest, error) {
	summary, err := c.fetchSummary(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]FriendRequest, 0, len(summary.Incoming))
	for _, item := range summary.Incoming {
		out = append(out, FriendRequest{
			AccountID:   trimSpace(item.AccountID),
			DisplayName: trimSpace(item.DisplayName),
			CreatedAt:   parseCreatedAt(item.Created),
		})
	}
	return out, nil
}

// ListOutgoingFriendRequests returns outgoing pending friend requests from summary endpoint.
// Endpoint (fnbr.js mapping): GET /friends/api/v1/{selfAccountId}/summary
func (c *Commands) ListOutgoingFriendRequests(ctx context.Context) ([]FriendRequest, error) {
	summary, err := c.fetchSummary(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]FriendRequest, 0, len(summary.Outgoing))
	for _, item := range summary.Outgoing {
		out = append(out, FriendRequest{
			AccountID:   trimSpace(item.AccountID),
			DisplayName: trimSpace(item.DisplayName),
			CreatedAt:   parseCreatedAt(item.Created),
		})
	}
	return out, nil
}

// CancelOutgoingFriendRequest aborts a previously sent request.
// Endpoint (fnbr.js mapping): DELETE /friends/api/v1/{selfAccountId}/friends/{targetAccountId}
func (c *Commands) CancelOutgoingFriendRequest(ctx context.Context, accountID string) error {
	return c.RemoveFriend(ctx, accountID)
}

// DeclineIncomingFriendRequest declines an incoming request.
// Endpoint (fnbr.js mapping): DELETE /friends/api/v1/{selfAccountId}/friends/{targetAccountId}
func (c *Commands) DeclineIncomingFriendRequest(ctx context.Context, accountID string) error {
	return c.RemoveFriend(ctx, accountID)
}

// RemoveAllFriends removes all currently listed friends using list->delete flow.
func (c *Commands) RemoveAllFriends(ctx context.Context) (BulkRemoveResult, error) {
	result := BulkRemoveResult{}

	friendsList, err := c.ListFriends(ctx)
	if err != nil {
		return result, err
	}

	for _, friend := range friendsList {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		accountID := trimSpace(friend.AccountID)
		if accountID == "" {
			continue
		}

		result.Attempted++
		if removeErr := c.RemoveFriend(ctx, accountID); removeErr != nil {
			result.Failed = append(result.Failed, BulkOperationError{AccountID: accountID, Err: removeErr})
			continue
		}
		result.Removed++
	}

	return result, nil
}

func (c *Commands) fetchSummary(ctx context.Context) (summaryResponse, error) {
	if err := c.validateConfig(); err != nil {
		return summaryResponse{}, err
	}

	resp, err := c.requestNoBody(ctx, http.MethodGet, fmt.Sprintf("/friends/api/v1/%s/summary", c.cfg.AccountID))
	if err != nil {
		return summaryResponse{}, err
	}

	if resp.StatusCode >= 300 {
		return summaryResponse{}, decodeAPIError(resp)
	}

	var payload summaryResponse
	if len(resp.Body) == 0 {
		return payload, nil
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return summaryResponse{}, fmt.Errorf("friends: decode summary: %w", err)
	}
	return payload, nil
}

func parseCreatedAt(raw string) *time.Time {
	raw = trimSpace(raw)
	if raw == "" {
		return nil
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05.000-0700",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err != nil {
			continue
		}
		t := parsed.UTC()
		return &t
	}

	return nil
}

func (c *Commands) requestNoBody(ctx context.Context, method, path string) (transporthttp.Response, error) {
	baseURL := c.cfg.BaseURL
	for len(baseURL) > 0 && baseURL[len(baseURL)-1] == '/' {
		baseURL = baseURL[:len(baseURL)-1]
	}

	return c.http.Request(ctx, transporthttp.Request{
		Method: method,
		URL:    baseURL + path,
	})
}

func (c *Commands) validateConfig() error {
	if c.http == nil {
		return ErrNotConfigured
	}

	if trimSpace(c.cfg.AccountID) == "" {
		return ErrNotConfigured
	}

	return nil
}

func decodeAPIError(resp transporthttp.Response) error {
	var payload struct {
		ErrorCode    string   `json:"errorCode"`
		ErrorMessage string   `json:"errorMessage"`
		MessageVars  []string `json:"messageVars"`
	}
	_ = json.Unmarshal(resp.Body, &payload)

	message := payload.ErrorMessage
	if message == "" {
		message = string(resp.Body)
	}

	return &APIError{
		StatusCode: resp.StatusCode,
		Code:       payload.ErrorCode,
		Message:    message,
	}
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end {
		switch s[start] {
		case ' ', '\t', '\n', '\r':
			start++
			continue
		default:
		}
		break
	}

	for end > start {
		switch s[end-1] {
		case ' ', '\t', '\n', '\r':
			end--
			continue
		default:
		}
		break
	}

	if start == 0 && end == len(s) {
		return s
	}

	return s[start:end]
}
