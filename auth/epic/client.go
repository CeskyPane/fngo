package epic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ceskypane/fngo/auth"
	"github.com/ceskypane/fngo/internal/backoff"
)

type Client struct {
	cfg   Config
	sleep func(time.Duration)
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.TokenURL == "" {
		cfg.TokenURL = DefaultTokenURL
	}

	if cfg.ClientID == "" {
		return nil, fmt.Errorf("auth/epic: client id is required")
	}

	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("auth/epic: client secret is required")
	}

	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}

	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}

	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = 100 * time.Millisecond
	}

	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 2 * time.Second
	}

	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = cfg.MinBackoff
	}

	return &Client{cfg: cfg, sleep: time.Sleep}, nil
}

func (c *Client) TokenByDeviceAuth(ctx context.Context, creds auth.DeviceAuth) (auth.TokenSet, error) {
	values := url.Values{}
	values.Set("grant_type", "device_auth")
	values.Set("device_id", creds.DeviceID)
	values.Set("account_id", creds.AccountID)
	values.Set("secret", creds.Secret)
	values.Set("token_type", "eg1")

	return c.tokenGrant(ctx, values)
}

func (c *Client) TokenByRefresh(ctx context.Context, refreshToken string) (auth.TokenSet, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)
	values.Set("token_type", "eg1")

	return c.tokenGrant(ctx, values)
}

func (c *Client) TokenByExchangeCode(ctx context.Context, grant ExchangeCodeGrant) (auth.TokenSet, error) {
	values := url.Values{}
	values.Set("grant_type", "exchange_code")
	values.Set("exchange_code", grant.ExchangeCode)
	values.Set("token_type", "eg1")

	return c.tokenGrant(ctx, values)
}

func (c *Client) TokenByAuthorizationCode(ctx context.Context, grant AuthorizationCodeGrant) (auth.TokenSet, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", grant.Code)
	values.Set("token_type", "eg1")

	return c.tokenGrant(ctx, values)
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        any    `json:"expires_in"`
	ExpiresAt        string `json:"expires_at"`
	RefreshExpiresIn any    `json:"refresh_expires"`
	RefreshExpiresAt string `json:"refresh_expires_at"`
	AccountID        string `json:"account_id"`
	ClientID         string `json:"client_id"`
	DisplayName      string `json:"displayName"`
}

type errorResponse struct {
	ErrorCode        string   `json:"errorCode"`
	ErrorMessage     string   `json:"errorMessage"`
	NumericErrorCode int      `json:"numericErrorCode"`
	MessageVars      []string `json:"messageVars"`
	Error            string   `json:"error"`
	ErrorDescription string   `json:"error_description"`
}

func (c *Client) tokenGrant(ctx context.Context, values url.Values) (auth.TokenSet, error) {
	attempt := 0

	for {
		attempt++

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(values.Encode()))
		if err != nil {
			return auth.TokenSet{}, err
		}

		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", "basic "+base64.StdEncoding.EncodeToString([]byte(c.cfg.ClientID+":"+c.cfg.ClientSecret)))

		resp, err := c.cfg.HTTPClient.Do(req)
		if err != nil {
			if attempt > c.cfg.MaxRetries+1 {
				return auth.TokenSet{}, err
			}

			if !c.sleepContext(ctx, c.retryBackoff(attempt-1)) {
				return auth.TokenSet{}, ctx.Err()
			}

			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return auth.TokenSet{}, readErr
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			tokens, parseErr := parseTokenResponse(body, time.Now().UTC())
			if parseErr != nil {
				return auth.TokenSet{}, parseErr
			}

			return tokens, nil
		}

		oauthErr := parseOAuthError(resp.StatusCode, body)
		if oauthErr.Retryable() && attempt <= c.cfg.MaxRetries+1 {
			delay := retryAfterDelay(resp.Header.Get("Retry-After"), c.retryBackoff(attempt-1))
			if !c.sleepContext(ctx, delay) {
				return auth.TokenSet{}, ctx.Err()
			}

			continue
		}

		return auth.TokenSet{}, oauthErr
	}
}

func parseTokenResponse(raw []byte, now time.Time) (auth.TokenSet, error) {
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return auth.TokenSet{}, err
	}

	tokens := auth.TokenSet{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		AccountID:    tr.AccountID,
		ClientID:     tr.ClientID,
		DisplayName:  tr.DisplayName,
	}

	expiresAt, err := parseExpiry(tr.ExpiresAt, tr.ExpiresIn, now)
	if err != nil {
		return auth.TokenSet{}, err
	}
	tokens.ExpiresAt = expiresAt

	if tr.RefreshExpiresAt != "" || tr.RefreshExpiresIn != nil {
		refreshExpiresAt, parseErr := parseExpiry(tr.RefreshExpiresAt, tr.RefreshExpiresIn, now)
		if parseErr == nil {
			tokens.RefreshExpiresAt = refreshExpiresAt
		}
	}

	if tokens.AccessToken == "" {
		return auth.TokenSet{}, fmt.Errorf("auth/epic: missing access token in oauth response")
	}

	if tokens.ExpiresAt.IsZero() {
		return auth.TokenSet{}, fmt.Errorf("auth/epic: missing expires_at in oauth response")
	}

	return tokens, nil
}

func parseExpiry(expiresAt string, expiresIn any, now time.Time) (time.Time, error) {
	if expiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, expiresAt)
		if err == nil {
			return parsed.UTC(), nil
		}
	}

	seconds, err := parseSeconds(expiresIn)
	if err != nil {
		return time.Time{}, err
	}

	return now.Add(time.Duration(seconds) * time.Second).UTC(), nil
}

func parseSeconds(v any) (int64, error) {
	switch n := v.(type) {
	case nil:
		return 0, fmt.Errorf("auth/epic: expiry is missing")
	case float64:
		return int64(n), nil
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	case string:
		if n == "" {
			return 0, fmt.Errorf("auth/epic: expiry is empty")
		}

		parsed, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0, err
		}

		return parsed, nil
	default:
		return 0, fmt.Errorf("auth/epic: unsupported expiry type %T", v)
	}
}

func parseOAuthError(status int, raw []byte) *OAuthError {
	var payload errorResponse
	_ = json.Unmarshal(raw, &payload)

	err := &OAuthError{
		StatusCode:       status,
		ErrorCode:        payload.ErrorCode,
		OAuthError:       payload.Error,
		ErrorDescription: payload.ErrorDescription,
		Message:          payload.ErrorMessage,
		NumericCode:      payload.NumericErrorCode,
		MessageVars:      payload.MessageVars,
	}

	if err.Message == "" && len(raw) > 0 {
		err.Message = string(raw)
	}

	return err
}

func retryAfterDelay(header string, fallback time.Duration) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return fallback
	}

	if sec, err := strconv.Atoi(header); err == nil {
		if sec < 0 {
			return fallback
		}

		return time.Duration(sec) * time.Second
	}

	if ts, err := http.ParseTime(header); err == nil {
		delay := time.Until(ts)
		if delay > 0 {
			return delay
		}
	}

	return fallback
}

func (c *Client) retryBackoff(attempt int) time.Duration {
	return backoff.Exponential(attempt, c.cfg.MinBackoff, c.cfg.MaxBackoff)
}

func (c *Client) sleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}

	done := make(chan struct{})
	go func() {
		c.sleep(d)
		close(done)
	}()

	select {
	case <-ctx.Done():
		return false
	case <-done:
		return true
	}
}
