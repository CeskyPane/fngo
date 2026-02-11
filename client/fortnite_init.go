package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	transporthttp "github.com/ceskypane/fngo/transport/http"
)

const (
	eulaBaseURL        = "https://eulatracking-public-service-prod-m.ol.epicgames.com/eulatracking/api/public/agreements/fn"
	grantAccessBaseURL = "https://fngw-mcp-gc-livefn.ol.epicgames.com/fortnite/api/game/v2/grant_access"
)

type eulaAgreement struct {
	Version int    `json:"version"`
	Locale  string `json:"locale"`
}

func (c *Client) initFortnite(ctx context.Context) error {
	if c == nil || c.httpClient == nil {
		return nil
	}

	if c.cfg.DisableFortniteInit {
		return nil
	}

	accountID := strings.TrimSpace(c.selfAccountID)
	if accountID == "" && c.tokenStore != nil {
		if tokens, ok, err := c.tokenStore.Load(ctx); err == nil && ok {
			accountID = strings.TrimSpace(tokens.AccountID)
		}
	}
	if accountID == "" {
		return fmt.Errorf("client: missing account id for fortnite init")
	}

	// Old JS flow (fnbr.js) accepts the Fortnite EULA and grants access on login.
	if err := c.acceptEULA(ctx, accountID); err != nil {
		return err
	}

	if err := c.grantAccess(ctx, accountID); err != nil {
		return err
	}

	return nil
}

func (c *Client) acceptEULA(ctx context.Context, accountID string) error {
	resp, err := c.httpClient.Request(ctx, transporthttp.Request{
		Method: "GET",
		URL:    fmt.Sprintf("%s/account/%s", eulaBaseURL, accountID),
	})
	if err != nil {
		return err
	}

	// Empty response means already accepted (matches fnbr.js behavior).
	if resp.StatusCode == 404 || len(resp.Body) == 0 {
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("eula fetch failed: status=%d", resp.StatusCode)
	}

	var data eulaAgreement
	if err := json.Unmarshal(resp.Body, &data); err != nil {
		return fmt.Errorf("eula fetch decode failed: %w", err)
	}

	if data.Version <= 0 {
		return nil
	}

	locale := strings.TrimSpace(data.Locale)
	acceptURL := fmt.Sprintf("%s/version/%d/account/%s/accept", eulaBaseURL, data.Version, accountID)
	if locale != "" {
		acceptURL += "?locale=" + url.QueryEscape(locale)
	}

	acceptResp, err := c.httpClient.Request(ctx, transporthttp.Request{
		Method: "POST",
		URL:    acceptURL,
	})
	if err != nil {
		return err
	}

	if acceptResp.StatusCode < 200 || acceptResp.StatusCode >= 300 {
		return fmt.Errorf("eula accept failed: status=%d", acceptResp.StatusCode)
	}

	return nil
}

func (c *Client) grantAccess(ctx context.Context, accountID string) error {
	resp, err := c.httpClient.Request(ctx, transporthttp.Request{
		Method: "POST",
		URL:    fmt.Sprintf("%s/%s", grantAccessBaseURL, accountID),
	})
	if err != nil {
		return err
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Epic started returning 404 for the legacy grant_access endpoint. fnbr.js treats this
	// call as best-effort; keep login working by ignoring not_found.
	if resp.StatusCode == 404 {
		return nil
	}

	// Old JS ignores "already has entitlement" errors.
	bodyRaw := strings.TrimSpace(string(resp.Body))
	bodyLower := strings.ToLower(bodyRaw)
	if strings.Contains(bodyLower, "already has the requested access entitlement") {
		return nil
	}

	if len(bodyRaw) > 200 {
		bodyRaw = bodyRaw[:200] + "..."
	}

	return fmt.Errorf("grant_access failed: status=%d body=%q", resp.StatusCode, bodyRaw)
}
