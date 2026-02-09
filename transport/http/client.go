package transporthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ceskypane/fngo/internal/backoff"
)

var (
	ErrUnauthorized  = errors.New("transport/http: unauthorized")
	ErrRefreshFailed = errors.New("transport/http: token refresh failed")
)

type TokenProvider interface {
	AccessToken(ctx context.Context) (string, error)
	Refresh(ctx context.Context) error
}

type Config struct {
	MaxRetries int
	MinBackoff time.Duration
	MaxBackoff time.Duration

	CorrelationIDHeader string
	CorrelationID       func() string
	UserAgent           string
}

type Request struct {
	Method        string
	URL           string
	Headers       map[string]string
	Body          []byte
	CorrelationID string
}

type Response struct {
	StatusCode int
	Headers    stdhttp.Header
	Body       []byte
}

type Client struct {
	httpClient    *stdhttp.Client
	tokenProvider TokenProvider
	cfg           Config
	sleep         func(time.Duration)
}

func NewClient(httpClient *stdhttp.Client, tokenProvider TokenProvider, cfg Config) *Client {
	if httpClient == nil {
		httpClient = stdhttp.DefaultClient
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

	if cfg.CorrelationIDHeader == "" {
		cfg.CorrelationIDHeader = "X-Correlation-Id"
	}

	return &Client{
		httpClient:    httpClient,
		tokenProvider: tokenProvider,
		cfg:           cfg,
		sleep:         time.Sleep,
	}
}

func (c *Client) Request(ctx context.Context, req Request) (Response, error) {
	var (
		attempt   int
		refreshed bool
	)

	for {
		attempt++

		token, err := c.accessToken(ctx)
		if err != nil {
			return Response{}, err
		}

		resp, err := c.doRequest(ctx, req, token)
		if err != nil {
			if attempt >= c.cfg.MaxRetries+1 {
				return Response{}, err
			}

			if !c.sleepContext(ctx, c.backoff(attempt-1)) {
				return Response{}, ctx.Err()
			}

			continue
		}

		if c.shouldRefreshForAuthFailure(resp) {
			if refreshed || c.tokenProvider == nil {
				return resp, ErrUnauthorized
			}

			refreshErr := c.tokenProvider.Refresh(ctx)
			if refreshErr != nil {
				return resp, fmt.Errorf("%w: %v", ErrRefreshFailed, refreshErr)
			}

			refreshed = true
			continue
		}

		if resp.StatusCode == stdhttp.StatusUnauthorized {
			return resp, ErrUnauthorized
		}

		if resp.StatusCode == stdhttp.StatusTooManyRequests {
			if attempt >= c.cfg.MaxRetries+1 {
				return resp, fmt.Errorf("transport/http: too many requests after retries")
			}

			delay := retryAfterDelay(resp.Headers.Get("Retry-After"), c.backoff(attempt-1))
			if !c.sleepContext(ctx, delay) {
				return Response{}, ctx.Err()
			}

			continue
		}

		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			if attempt >= c.cfg.MaxRetries+1 {
				return resp, fmt.Errorf("transport/http: upstream %d after retries", resp.StatusCode)
			}

			if !c.sleepContext(ctx, c.backoff(attempt-1)) {
				return Response{}, ctx.Err()
			}

			continue
		}

		return resp, nil
	}
}

func (c *Client) doRequest(ctx context.Context, req Request, accessToken string) (Response, error) {
	reader := bytes.NewReader(req.Body)
	httpReq, err := stdhttp.NewRequestWithContext(ctx, req.Method, req.URL, reader)
	if err != nil {
		return Response{}, err
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	if accessToken != "" {
		if httpReq.Header.Get("Authorization") == "" {
			httpReq.Header.Set("Authorization", "Bearer "+accessToken)
		}
	}

	if c.cfg.UserAgent != "" {
		if httpReq.Header.Get("User-Agent") == "" {
			httpReq.Header.Set("User-Agent", c.cfg.UserAgent)
		}
	}

	correlationID := req.CorrelationID
	if correlationID == "" && c.cfg.CorrelationID != nil {
		correlationID = c.cfg.CorrelationID()
	}
	if correlationID != "" {
		httpReq.Header.Set(c.cfg.CorrelationIDHeader, correlationID)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, err
	}

	return Response{
		StatusCode: httpResp.StatusCode,
		Headers:    httpResp.Header.Clone(),
		Body:       body,
	}, nil
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	if c.tokenProvider == nil {
		return "", nil
	}

	token, err := c.tokenProvider.AccessToken(ctx)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (c *Client) backoff(attempt int) time.Duration {
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

type epicErrorBody struct {
	ErrorCode string `json:"errorCode"`
	Error     string `json:"error"`
}

func (c *Client) shouldRefreshForAuthFailure(resp Response) bool {
	parsed := parseEpicErrorBody(resp.Body)
	if parsed != nil {
		if isEpicAuthFailure(parsed) {
			return true
		}

		if resp.StatusCode == stdhttp.StatusUnauthorized {
			return false
		}
	}

	if resp.StatusCode == stdhttp.StatusUnauthorized {
		return true
	}

	return false
}

func parseEpicErrorBody(body []byte) *epicErrorBody {
	if len(body) == 0 {
		return nil
	}

	var payload epicErrorBody
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}

	if payload.ErrorCode == "" && payload.Error == "" {
		return nil
	}

	return &payload
}

func isEpicAuthFailure(body *epicErrorBody) bool {
	if body == nil {
		return false
	}

	code := strings.ToLower(body.ErrorCode)
	oauthErr := strings.ToLower(body.Error)

	if oauthErr == "invalid_token" {
		return true
	}

	authCodes := []string{
		"errors.com.epicgames.common.oauth.invalid_token",
		"errors.com.epicgames.common.authentication.token_verification_failed",
	}
	for _, known := range authCodes {
		if code == known {
			return true
		}
	}

	if strings.Contains(code, "invalid_token") {
		return true
	}

	return false
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

	if ts, err := stdhttp.ParseTime(header); err == nil {
		delay := time.Until(ts)
		if delay > 0 {
			return delay
		}
	}

	return fallback
}
