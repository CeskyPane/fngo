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
