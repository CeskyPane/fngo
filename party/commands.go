package party

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ceskypane/fngo/internal/backoff"
	"github.com/ceskypane/fngo/logging"
	"github.com/ceskypane/fngo/matchmaking"
	transporthttp "github.com/ceskypane/fngo/transport/http"
)

var (
	ErrNotConfigured  = errors.New("party: commands not configured")
	ErrPartyNotFound  = errors.New("party: no current party")
	ErrNotInParty     = ErrPartyNotFound
	ErrMemberNotFound = errors.New("party: member not found")
	ErrNotCaptain     = errors.New("party: client is not captain")
	ErrPatchConflict  = errors.New("party: patch conflict after retries")
	ErrInviteNotFound = errors.New("party: invite not found")
)

type APIError struct {
	StatusCode  int
	Code        string
	Message     string
	MessageVars []string
}

func (e *APIError) Error() string {
	if e == nil {
		return "party: api error"
	}

	if e.Code != "" {
		return fmt.Sprintf("party api error status=%d code=%s message=%s", e.StatusCode, e.Code, e.Message)
	}

	return fmt.Sprintf("party api error status=%d message=%s", e.StatusCode, e.Message)
}

type Commands struct {
	state *State
	http  HTTPClient
	cfg   Config

	now   func() time.Time
	sleep func(time.Duration)
	log   logging.Logger

	opMu sync.Mutex
}

func NewCommands(state *State, httpClient HTTPClient, cfg Config) *Commands {
	if state == nil {
		state = NewState()
	}

	defaults := DefaultConfig()
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaults.BaseURL
	}

	if cfg.Platform == "" {
		cfg.Platform = defaults.Platform
	}

	if cfg.BuildID == "" {
		cfg.BuildID = defaults.BuildID
	}

	if cfg.MaxPatchRetries <= 0 {
		cfg.MaxPatchRetries = defaults.MaxPatchRetries
	}

	if cfg.MinPatchBackoff <= 0 {
		cfg.MinPatchBackoff = defaults.MinPatchBackoff
	}

	if cfg.MaxPatchBackoff <= 0 {
		cfg.MaxPatchBackoff = defaults.MaxPatchBackoff
	}

	if cfg.MaxPatchBackoff < cfg.MinPatchBackoff {
		cfg.MaxPatchBackoff = cfg.MinPatchBackoff
	}

	return &Commands{
		state: state,
		http:  httpClient,
		cfg:   cfg,
		now:   time.Now,
		sleep: time.Sleep,
		log:   logging.With(cfg.Logger),
	}
}

func (c *Commands) SetIdentity(accountID, displayName string) {
	if accountID != "" {
		c.cfg.AccountID = accountID
	}

	if displayName != "" {
		c.cfg.DisplayName = displayName
	}
}

func (c *Commands) SyncCurrentParty(ctx context.Context) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.validateConfig(); err != nil {
		return err
	}

	party, err := c.getCurrentPartyForAccount(ctx, c.cfg.AccountID)
	if err != nil {
		return err
	}

	if party == nil {
		c.state.Replace(Party{})
		return nil
	}

	c.state.Replace(*party)
	return nil
}

func (c *Commands) EnsureParty(ctx context.Context) (*Party, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	return c.ensurePartyLocked(ctx)
}

func (c *Commands) ResyncAfterReconnect(ctx context.Context) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	_, err := c.ensurePartyLocked(ctx)
	return err
}

func (c *Commands) ensurePartyLocked(ctx context.Context) (*Party, error) {
	if err := c.validateConfig(); err != nil {
		return nil, err
	}

	party, err := c.getCurrentPartyForAccount(ctx, c.cfg.AccountID)
	if err != nil {
		return nil, err
	}

	if party != nil {
		c.state.Replace(*party)
		return party, nil
	}

	created, err := c.createPartyLocked(ctx)
	if err != nil {
		return nil, err
	}

	c.state.Replace(*created)
	return created, nil
}

func (c *Commands) CreateParty(ctx context.Context) (*Party, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	return c.createPartyLocked(ctx)
}

func (c *Commands) createPartyLocked(ctx context.Context) (*Party, error) {
	if err := c.validateConfig(); err != nil {
		return nil, err
	}

	connectionID := c.cfg.ConnectionID
	if connectionID == "" {
		connectionID = fmt.Sprintf("%s@prod.ol.epicgames.com", c.cfg.AccountID)
	}

	payload := map[string]any{
		"config": map[string]any{
			"join_confirmation": false,
			"joinability":       "OPEN",
			"max_size":          16,
		},
		"join_info": map[string]any{
			"connection": map[string]any{
				"id": connectionID,
				"meta": map[string]any{
					"urn:epic:conn:platform_s": c.cfg.Platform,
					"urn:epic:conn:type_s":     "game",
				},
				"yield_leadership": false,
			},
			"meta": map[string]any{
				"urn:epic:member:dn_s": c.cfg.DisplayName,
			},
		},
		"meta": map[string]any{
			"urn:epic:cfg:party-type-id_s":       "default",
			"urn:epic:cfg:build-id_s":            c.cfg.BuildID,
			"urn:epic:cfg:join-request-action_s": "Manual",
			"urn:epic:cfg:chat-enabled_b":        "true",
			"urn:epic:cfg:can-join_b":            "true",
		},
	}

	resp, err := c.requestJSON(ctx, http.MethodPost, "/parties", payload)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 300 {
		if isUserHasParty(resp) {
			party, currentErr := c.getCurrentPartyForAccount(ctx, c.cfg.AccountID)
			if currentErr != nil {
				return nil, currentErr
			}

			if party != nil {
				return party, nil
			}
		}

		return nil, decodeAPIError(resp)
	}

	party, err := decodeRemotePartyEnvelope(resp.Body)
	if err != nil {
		return nil, err
	}

	return party, nil
}

func (c *Commands) JoinParty(ctx context.Context, id string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	return c.joinPartyLocked(ctx, id)
}

func (c *Commands) joinPartyLocked(ctx context.Context, id string) error {
	if err := c.validateConfig(); err != nil {
		return err
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("party: party id is required")
	}

	snapshot := c.state.Snapshot()
	if snapshot != nil && snapshot.ID == id {
		return nil
	}

	if snapshot != nil && snapshot.ID != "" && snapshot.ID != id {
		if err := c.leavePartyLocked(ctx, false); err != nil && !errors.Is(err, ErrPartyNotFound) {
			return err
		}
	}

	payload := map[string]any{
		"connection": map[string]any{
			"id": c.connectionID(),
			"meta": map[string]any{
				"urn:epic:conn:platform_s": c.cfg.Platform,
				"urn:epic:conn:type_s":     "game",
			},
			"yield_leadership": false,
		},
		"meta": map[string]any{
			"urn:epic:member:dn_s": c.cfg.DisplayName,
		},
	}

	resp, err := c.requestJSON(ctx, http.MethodPost, fmt.Sprintf("/parties/%s/members/%s/join", id, c.cfg.AccountID), payload)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		return decodeAPIError(resp)
	}

	party, err := c.getParty(ctx, id)
	if err != nil {
		return err
	}

	c.state.Replace(*party)
	return nil
}

func (c *Commands) JoinPartyByMemberID(ctx context.Context, memberAccountID string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.validateConfig(); err != nil {
		return err
	}

	memberAccountID = trimSpace(memberAccountID)
	if memberAccountID == "" {
		return fmt.Errorf("party: member account id is required")
	}

	party, err := c.getCurrentPartyForAccount(ctx, memberAccountID)
	if err != nil {
		return err
	}

	if party == nil || party.ID == "" {
		return ErrPartyNotFound
	}

	return c.joinPartyLocked(ctx, party.ID)
}

func (c *Commands) SendJoinRequestToMember(ctx context.Context, memberAccountID string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.validateConfig(); err != nil {
		return err
	}

	memberAccountID = trimSpace(memberAccountID)
	if memberAccountID == "" {
		return fmt.Errorf("party: member account id is required")
	}

	payload := map[string]any{
		"urn:epic:invite:platformdata_s": "",
	}

	resp, err := c.requestJSON(ctx, http.MethodPost, fmt.Sprintf("/members/%s/intentions/%s", memberAccountID, c.cfg.AccountID), payload)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		return decodeAPIError(resp)
	}

	return nil
}

func (c *Commands) JoinPartyInviteFrom(ctx context.Context, pingerAccountID string) (string, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.validateConfig(); err != nil {
		return "", err
	}

	pingerAccountID = trimSpace(pingerAccountID)
	if pingerAccountID == "" {
		return "", fmt.Errorf("party: pinger account id is required")
	}

	resp, err := c.requestNoBody(ctx, http.MethodGet, fmt.Sprintf("/user/%s/pings/%s/parties", c.cfg.AccountID, pingerAccountID))
	if err != nil {
		return "", err
	}

	if resp.StatusCode >= 300 {
		return "", decodeAPIError(resp)
	}

	var payload []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return "", err
	}

	if len(payload) == 0 || trimSpace(payload[0].ID) == "" {
		return "", ErrInviteNotFound
	}

	partyID := trimSpace(payload[0].ID)
	if err := c.joinPartyLocked(ctx, partyID); err != nil {
		return "", err
	}
	return partyID, nil
}

func (c *Commands) LeaveParty(ctx context.Context) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	return c.leavePartyLocked(ctx, !c.cfg.DisableAutoCreateAfterLeave)
}

func (c *Commands) PromoteMember(ctx context.Context, partyID string, accountID string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	if err := c.validateConfig(); err != nil {
		return err
	}

	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("party: account id is required")
	}

	snapshot := c.state.Snapshot()
	if snapshot == nil || snapshot.ID == "" {
		return ErrNotInParty
	}

	partyID = strings.TrimSpace(partyID)
	if partyID == "" {
		partyID = snapshot.ID
	}

	if partyID == "" {
		return ErrNotInParty
	}

	resp, err := c.requestNoBody(ctx, http.MethodPost, fmt.Sprintf("/parties/%s/members/%s/promote", partyID, accountID))
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		return mapPromoteError(resp)
	}

	party, refreshErr := c.refreshPartyState(ctx, partyID)
	if refreshErr != nil {
		return refreshErr
	}

	if party != nil && party.CaptainID == "" {
		party.CaptainID = accountID
		c.state.Replace(*party)
	}

	c.log.Info("party member promoted", logging.F("party_id", partyID), logging.F("account_id", accountID))
	return nil
}

func (c *Commands) leavePartyLocked(ctx context.Context, createAfter bool) error {
	if err := c.validateConfig(); err != nil {
		return err
	}

	snapshot := c.state.Snapshot()
	if snapshot == nil || snapshot.ID == "" {
		return ErrPartyNotFound
	}

	resp, err := c.requestNoBody(ctx, http.MethodDelete, fmt.Sprintf("/parties/%s/members/%s", snapshot.ID, c.cfg.AccountID))
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		return decodeAPIError(resp)
	}

	c.state.Replace(Party{})

	if !createAfter {
		return nil
	}

	_, err = c.ensurePartyLocked(ctx)
	return err
}

func (c *Commands) SetReady(ctx context.Context, state ReadyState) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	intent := PatchIntent{Ready: &state}
	return c.applyIntentWithRetry(ctx, intent)
}

func (c *Commands) SetPlaylist(ctx context.Context, req matchmaking.PlaylistRequest) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	intent := PatchIntent{Playlist: &req}
	return c.applyIntentWithRetry(ctx, intent)
}

func (c *Commands) SetCustomKey(ctx context.Context, key string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	intent := PatchIntent{CustomKey: &key}
	return c.applyIntentWithRetry(ctx, intent)
}

func (c *Commands) SetOutfit(ctx context.Context, outfitID string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	outfitID = strings.TrimSpace(outfitID)
	if outfitID == "" {
		return fmt.Errorf("party: outfit id is required")
	}

	intent := PatchIntent{OutfitID: &outfitID}
	return c.applyIntentWithRetry(ctx, intent)
}

func (c *Commands) SetEmote(ctx context.Context, emoteID string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	emoteID = strings.TrimSpace(emoteID)
	if emoteID == "" {
		return fmt.Errorf("party: emote id is required")
	}

	intent := PatchIntent{EmoteID: &emoteID}
	return c.applyIntentWithRetry(ctx, intent)
}

func (c *Commands) ClearEmote(ctx context.Context) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	intent := PatchIntent{ClearEmote: true}
	return c.applyIntentWithRetry(ctx, intent)
}

func (c *Commands) SetLoadout(ctx context.Context, loadout Loadout) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	intent := PatchIntent{Loadout: &loadout}
	return c.applyIntentWithRetry(ctx, intent)
}

func (c *Commands) applyIntentWithRetry(ctx context.Context, intent PatchIntent) error {
	if err := c.validateConfig(); err != nil {
		return err
	}

	builder := NewIntentPatchBuilder(c.cfg.AccountID)
	c.log.Debug("party patch start", logging.F("account_id", c.cfg.AccountID), logging.F("max_retries", c.cfg.MaxPatchRetries))

	for attempt := 0; attempt <= c.cfg.MaxPatchRetries; attempt++ {
		snapshot := c.state.Snapshot()
		if snapshot == nil || snapshot.ID == "" {
			return ErrPartyNotFound
		}

		member, ok := snapshot.Members[c.cfg.AccountID]
		if !ok {
			return ErrPartyNotFound
		}

		built, err := builder.BuildIntents(snapshot, intent, c.now().UTC())
		if err != nil {
			return err
		}

		if len(built.PartyMetaUpdates) == 0 && len(built.MemberMetaUpdates) == 0 {
			return nil
		}

		stale := false

		if len(built.PartyMetaUpdates) > 0 {
			c.log.Debug("party patch request", logging.F("party_id", snapshot.ID), logging.F("party_revision", snapshot.Revision), logging.F("attempt", attempt))
			partyResp, partyErr := c.sendPartyPatch(ctx, snapshot.ID, snapshot.Revision, built.PartyMetaUpdates)
			if partyErr != nil {
				return partyErr
			}

			if isStaleRevision(partyResp) {
				c.log.Warn("party patch stale revision", logging.F("party_id", snapshot.ID), logging.F("attempt", attempt), logging.F("scope", "party"))
				stale = true
			} else if partyResp.StatusCode >= 300 {
				return decodeAPIError(partyResp)
			}
		}

		if !stale && len(built.MemberMetaUpdates) > 0 {
			c.log.Debug("member patch request", logging.F("party_id", snapshot.ID), logging.F("member_revision", member.Revision), logging.F("attempt", attempt))
			memberResp, memberErr := c.sendMemberPatch(ctx, snapshot.ID, member.Revision, built.MemberMetaUpdates)
			if memberErr != nil {
				return memberErr
			}

			if isStaleRevision(memberResp) {
				c.log.Warn("member patch stale revision", logging.F("party_id", snapshot.ID), logging.F("attempt", attempt), logging.F("scope", "member"))
				stale = true
			} else if memberResp.StatusCode >= 300 {
				return decodeAPIError(memberResp)
			}
		}

		if stale {
			if _, refreshErr := c.refreshPartyState(ctx, snapshot.ID); refreshErr != nil {
				return refreshErr
			}

			if attempt == c.cfg.MaxPatchRetries {
				return ErrPatchConflict
			}

			delay := backoff.Exponential(attempt, c.cfg.MinPatchBackoff, c.cfg.MaxPatchBackoff)
			if !c.sleepContext(ctx, delay) {
				return ctx.Err()
			}

			continue
		}

		if _, refreshErr := c.refreshPartyState(ctx, snapshot.ID); refreshErr != nil {
			return refreshErr
		}

		c.log.Info("party patch applied", logging.F("party_id", snapshot.ID), logging.F("attempt", attempt))
		return nil
	}

	return ErrPatchConflict
}

func (c *Commands) sendPartyPatch(ctx context.Context, partyID string, revision int64, updates map[string]any) (transporthttp.Response, error) {
	payload := map[string]any{
		"config": map[string]any{
			"join_confirmation": false,
			"joinability":       "OPEN",
			"max_size":          16,
		},
		"meta": map[string]any{
			"delete": []string{},
			"update": buildMetaPatch(updates),
		},
		"party_state_overridden": map[string]any{},
		"party_privacy_type":     "OPEN",
		"party_type":             "DEFAULT",
		"party_sub_type":         "default",
		"max_number_of_members":  16,
		"invite_ttl_seconds":     14400,
		"revision":               revision,
	}

	return c.requestJSON(ctx, http.MethodPatch, fmt.Sprintf("/parties/%s", partyID), payload)
}

func (c *Commands) sendMemberPatch(ctx context.Context, partyID string, memberRevision int64, updates map[string]any) (transporthttp.Response, error) {
	payload := map[string]any{
		"delete":   []string{},
		"revision": memberRevision,
		"update":   buildMetaPatch(updates),
	}

	return c.requestJSON(ctx, http.MethodPatch, fmt.Sprintf("/parties/%s/members/%s/meta", partyID, c.cfg.AccountID), payload)
}

func (c *Commands) refreshPartyState(ctx context.Context, partyID string) (*Party, error) {
	party, err := c.getParty(ctx, partyID)
	if err != nil {
		return nil, err
	}

	c.state.Replace(*party)
	return party, nil
}

func (c *Commands) getCurrentParty(ctx context.Context) (*Party, error) {
	return c.getCurrentPartyForAccount(ctx, c.cfg.AccountID)
}

func (c *Commands) getCurrentPartyForAccount(ctx context.Context, accountID string) (*Party, error) {
	resp, err := c.requestNoBody(ctx, http.MethodGet, fmt.Sprintf("/user/%s", accountID))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 300 {
		return nil, decodeAPIError(resp)
	}

	var envelope struct {
		Current []remoteParty `json:"current"`
	}
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &envelope); err != nil {
			return nil, err
		}
	}

	if len(envelope.Current) == 0 {
		return nil, nil
	}

	party := mapRemoteParty(envelope.Current[0])
	return &party, nil
}

func (c *Commands) getParty(ctx context.Context, id string) (*Party, error) {
	resp, err := c.requestNoBody(ctx, http.MethodGet, fmt.Sprintf("/parties/%s", id))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 300 {
		return nil, decodeAPIError(resp)
	}

	var rp remoteParty
	if err := json.Unmarshal(resp.Body, &rp); err != nil {
		return nil, err
	}

	party := mapRemoteParty(rp)
	return &party, nil
}

func (c *Commands) requestNoBody(ctx context.Context, method, path string) (transporthttp.Response, error) {
	return c.http.Request(ctx, transporthttp.Request{
		Method: method,
		URL:    strings.TrimRight(c.cfg.BaseURL, "/") + path,
	})
}

func (c *Commands) requestJSON(ctx context.Context, method, path string, payload any) (transporthttp.Response, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return transporthttp.Response{}, err
	}

	return c.http.Request(ctx, transporthttp.Request{
		Method: method,
		URL:    strings.TrimRight(c.cfg.BaseURL, "/") + path,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: raw,
	})
}

func (c *Commands) validateConfig() error {
	if c.http == nil {
		return ErrNotConfigured
	}

	if c.cfg.AccountID == "" {
		return ErrNotConfigured
	}

	return nil
}

func (c *Commands) connectionID() string {
	if strings.TrimSpace(c.cfg.ConnectionID) != "" {
		return c.cfg.ConnectionID
	}

	if strings.TrimSpace(c.cfg.AccountID) == "" {
		return ""
	}

	return fmt.Sprintf("%s@prod.ol.epicgames.com", c.cfg.AccountID)
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
		StatusCode:  resp.StatusCode,
		Code:        payload.ErrorCode,
		Message:     message,
		MessageVars: payload.MessageVars,
	}
}

func isStaleRevision(resp transporthttp.Response) bool {
	if resp.StatusCode < 400 {
		return false
	}

	var payload struct {
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return false
	}

	return payload.ErrorCode == "errors.com.epicgames.social.party.stale_revision"
}

func isUserHasParty(resp transporthttp.Response) bool {
	if resp.StatusCode < 400 {
		return false
	}

	var payload struct {
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return false
	}

	return payload.ErrorCode == "errors.com.epicgames.social.party.user_has_party"
}

func mapPromoteError(resp transporthttp.Response) error {
	apiErr := decodeAPIError(resp)
	typed, ok := apiErr.(*APIError)
	if !ok {
		return apiErr
	}

	code := strings.ToLower(strings.TrimSpace(typed.Code))

	if code == "errors.com.epicgames.social.party.party_change_forbidden" || typed.StatusCode == http.StatusForbidden {
		return ErrNotCaptain
	}

	if strings.Contains(code, "member_not_found") {
		return ErrMemberNotFound
	}

	if strings.Contains(code, "party_not_found") {
		return ErrNotInParty
	}

	if typed.StatusCode == http.StatusNotFound {
		return ErrMemberNotFound
	}

	return apiErr
}

func decodeRemotePartyEnvelope(raw []byte) (*Party, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("party: empty create party response")
	}

	var direct remoteParty
	if err := json.Unmarshal(raw, &direct); err == nil && direct.ID != "" {
		party := mapRemoteParty(direct)
		return &party, nil
	}

	var envelope struct {
		Current []remoteParty `json:"current"`
		Party   *remoteParty  `json:"party"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}

	if envelope.Party != nil && envelope.Party.ID != "" {
		party := mapRemoteParty(*envelope.Party)
		return &party, nil
	}

	if len(envelope.Current) > 0 {
		party := mapRemoteParty(envelope.Current[0])
		return &party, nil
	}

	return nil, fmt.Errorf("party: create party response missing party payload")
}

func mapRemoteParty(rp remoteParty) Party {
	party := Party{
		ID:        rp.ID,
		Revision:  rp.Revision,
		Meta:      normalizeMetaMap(rp.Meta),
		Members:   make(map[string]Member, len(rp.Members)),
		UpdatedAt: time.Now().UTC(),
	}

	party.CustomKey = party.Meta["Default:CustomMatchKey_s"]
	party.RegionID = party.Meta["Default:RegionId_s"]
	party.Playlist = extractPlaylist(party.Meta["Default:SelectedIsland_j"])

	for _, rm := range rp.Members {
		joinedAt := time.Time{}
		if rm.JoinedAt != "" {
			parsed, err := time.Parse(time.RFC3339, rm.JoinedAt)
			if err == nil {
				joinedAt = parsed.UTC()
			}
		}

		meta := normalizeMetaMap(rm.Meta)
		member := Member{
			AccountID:   rm.AccountID,
			DisplayName: rm.AccountDN,
			Role:        rm.Role,
			JoinedAt:    joinedAt,
			Revision:    rm.Revision,
			Meta:        meta,
			Ready:       extractReady(meta["Default:LobbyState_j"]),
			Loadout:     extractLoadout(meta["Default:AthenaCosmeticLoadout_j"], meta["Default:FrontendEmote_j"]),
		}
		party.Members[rm.AccountID] = member

		if rm.Role == "CAPTAIN" {
			party.CaptainID = rm.AccountID
		}
	}

	return party
}

func extractReady(raw string) ReadyState {
	data := decodeJSONMap(raw)
	lobbyState := decodeJSONMapValue(data, "LobbyState")
	if fmt.Sprint(lobbyState["gameReadiness"]) == string(ReadyStateReady) {
		return ReadyStateReady
	}

	return ReadyStateNotReady
}

func extractLoadout(raw string, emoteRaw string) Loadout {
	loadout := Loadout{}
	data := decodeJSONMap(raw)
	root := decodeJSONMapValue(data, "AthenaCosmeticLoadout")

	loadout.Character = normalizePrintable(root["characterPrimaryAssetId"])
	loadout.Backpack = normalizePrintable(root["backpackDef"])
	loadout.Pickaxe = normalizePrintable(root["pickaxeDef"])

	emote := decodeJSONMap(emoteRaw)
	emoteRoot := decodeJSONMapValue(emote, "FrontendEmote")
	loadout.Emote = normalizePrintable(emoteRoot["pickable"])

	return loadout
}

func extractPlaylist(raw string) string {
	selected := decodeJSONMap(raw)
	island := decodeJSONMapValue(selected, "SelectedIsland")
	link := island["LinkId"]

	linkString := normalizePrintable(link)
	if linkString != "" && linkString != "{}" {
		return linkString
	}

	linkMap := decodeJSONMapValue(island, "linkId")
	mnemonic := normalizePrintable(linkMap["mnemonic"])
	if mnemonic != "" {
		return mnemonic
	}

	return ""
}

func normalizePrintable(value any) string {
	if value == nil {
		return ""
	}

	s := fmt.Sprint(value)
	if s == "<nil>" {
		return ""
	}

	return s
}

func (c *Commands) sleepContext(ctx context.Context, d time.Duration) bool {
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

type remoteParty struct {
	ID       string              `json:"id"`
	Revision int64               `json:"revision"`
	Meta     map[string]any      `json:"meta"`
	Members  []remotePartyMember `json:"members"`
}

type remotePartyMember struct {
	AccountID string         `json:"account_id"`
	AccountDN string         `json:"account_dn"`
	Role      string         `json:"role"`
	JoinedAt  string         `json:"joined_at"`
	Revision  int64          `json:"revision"`
	Meta      map[string]any `json:"meta"`
}

func parseRevisionFromStale(resp transporthttp.Response) int64 {
	var payload struct {
		MessageVars []string `json:"messageVars"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return 0
	}

	if len(payload.MessageVars) < 2 {
		return 0
	}

	revision, err := strconv.ParseInt(payload.MessageVars[1], 10, 64)
	if err != nil {
		return 0
	}

	return revision
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
