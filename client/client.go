package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ceskypane/fngo/auth"
	"github.com/ceskypane/fngo/auth/epic"
	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/friends"
	"github.com/ceskypane/fngo/logging"
	"github.com/ceskypane/fngo/matchmaking"
	"github.com/ceskypane/fngo/party"
	transporthttp "github.com/ceskypane/fngo/transport/http"
	"github.com/ceskypane/fngo/xmpp"
)

type Client struct {
	cfg Config
	log logging.Logger

	bus *events.Bus
	sub *events.Subscription

	oauthClient epic.EpicOAuthClient
	tokenStore  auth.TokenStore
	deviceStore auth.DeviceAuthStore

	tokenProvider *epicTokenProvider
	authScheduler *auth.RefreshScheduler
	httpClient    *transporthttp.Client
	xmppClient    *xmpp.Client

	partyState    *party.State
	partyCommands *party.Commands
	partyDecoder  *party.XMPPDecoder

	friendsCommands *friends.Commands
	friendsDecoder  *friends.XMPPDecoder

	selfAccountID string

	runMu      sync.Mutex
	runCancel  context.CancelFunc
	startedCtx context.Context

	watchMu      sync.Mutex
	watchSub     *events.Subscription
	watchWG      sync.WaitGroup
	watchStarted bool
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.EventBuffer <= 0 {
		cfg.EventBuffer = 64
	}

	bus := events.NewBus()
	sub, err := bus.Subscribe(cfg.EventBuffer)
	if err != nil {
		return nil, err
	}

	tokenStore := cfg.TokenStore
	if tokenStore == nil {
		tokenStore = auth.NewMemoryTokenStore()
	}

	log := logging.With(cfg.Logger)
	cfg.Logger = log

	state := party.NewState()

	c := &Client{
		cfg:            cfg,
		log:            log,
		bus:            bus,
		sub:            sub,
		tokenStore:     tokenStore,
		deviceStore:    cfg.DeviceAuthStore,
		partyState:     state,
		partyDecoder:   party.NewXMPPDecoder(bus, state),
		friendsDecoder: friends.NewXMPPDecoder(bus),
	}

	if cfg.OAuth.ClientID != "" || cfg.OAuth.ClientSecret != "" {
		oauthCfg := cfg.OAuth
		if oauthCfg.HTTPClient == nil {
			oauthCfg.HTTPClient = cfg.HTTPClient
		}

		oauthClient, oauthErr := epic.NewClient(oauthCfg)
		if oauthErr != nil {
			return nil, oauthErr
		}

		c.oauthClient = oauthClient
	}

	c.tokenProvider = &epicTokenProvider{store: tokenStore, oauth: c.oauthClient}
	if cfg.Party.Logger == nil {
		cfg.Party.Logger = log
	}

	c.httpClient = transporthttp.NewClient(cfg.HTTPClient, c.tokenProvider, cfg.HTTP)
	c.partyCommands = party.NewCommands(state, c.httpClient, cfg.Party)
	c.friendsCommands = friends.NewCommands(c.httpClient, cfg.Friends)

	if c.oauthClient != nil {
		refreshCfg := auth.SchedulerConfig{
			TokenStore: tokenStore,
			Refresher:  &oauthRefresher{provider: c.tokenProvider},
			Bus:        bus,
		}
		if cfg.AuthScheduler != nil {
			refreshCfg = *cfg.AuthScheduler
			refreshCfg.TokenStore = tokenStore
			refreshCfg.Refresher = &oauthRefresher{provider: c.tokenProvider}
			refreshCfg.Bus = bus
		}

		if refreshCfg.Logger == nil {
			refreshCfg.Logger = log
		}

		scheduler, schedErr := auth.NewRefreshScheduler(refreshCfg)
		if schedErr != nil {
			return nil, schedErr
		}

		c.authScheduler = scheduler
	}

	return c, nil
}

func (c *Client) Login(ctx context.Context) error {
	c.runMu.Lock()
	if c.runCancel != nil {
		c.runMu.Unlock()
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.runCancel = cancel
	c.startedCtx = runCtx
	c.runMu.Unlock()

	if err := c.loginWithDeviceAuth(runCtx); err != nil {
		return err
	}

	if err := c.initFortnite(runCtx); err != nil {
		return err
	}

	if c.authScheduler != nil {
		go c.authScheduler.Run(runCtx)
	}

	if c.cfg.EnableXMPP {
		xmppCfg := c.cfg.XMPP
		tokens, ok, err := c.tokenStore.Load(runCtx)
		if err != nil {
			return err
		}

		if ok {
			if xmppCfg.JID == "" && tokens.AccountID != "" {
				xmppCfg.JID = fmt.Sprintf("%s@prod.ol.epicgames.com", tokens.AccountID)
			}

			if xmppCfg.Username == "" {
				xmppCfg.Username = tokens.AccountID
			}
		}

		if xmppCfg.Domain == "" {
			xmppCfg.Domain = "prod.ol.epicgames.com"
		}

		// Default resource used by xmpp.NewClient; set explicitly so we can derive connection id for party-service.
		if xmppCfg.Resource == "" {
			xmppCfg.Resource = "V2:Fortnite:WIN::FNGO"
		}

		dispatchers := make([]xmpp.StanzaDispatcher, 0, 6)
		dispatchers = append(dispatchers, c.friendsDecoder, c.partyDecoder)
		if xmppCfg.StanzaDispatcher != nil {
			dispatchers = append(dispatchers, xmppCfg.StanzaDispatcher)
		}
		if c.cfg.XMPPStanzaDispatcher != nil {
			dispatchers = append(dispatchers, c.cfg.XMPPStanzaDispatcher)
		}
		xmppCfg.StanzaDispatcher = &chainStanzaDispatcher{items: dispatchers}
		if xmppCfg.Logger == nil {
			xmppCfg.Logger = c.log
		}

		c.xmppClient = xmpp.NewClient(xmppCfg, c.bus, c.tokenProvider, c.cfg.XMPPDispatcher, c.cfg.XMPPDialer)
		if err := c.startXMPPWatcher(runCtx); err != nil {
			return err
		}

		sub, err := c.bus.Subscribe(64)
		if err != nil {
			return err
		}
		defer sub.Cancel()

		if err := c.xmppClient.Connect(runCtx); err != nil {
			return err
		}

		// Wait until XMPP handshake completes (xmpp.Client.Connect returns immediately after starting
		// the supervisor goroutine). Party-service operations can fail with "user_is_offline" until
		// the XMPP session is established.
		var lastXMPPErr error
		for {
			select {
			case <-runCtx.Done():
				if lastXMPPErr != nil {
					return fmt.Errorf("xmpp connect timeout: %w", lastXMPPErr)
				}
				return runCtx.Err()
			case evt, ok := <-sub.C:
				if !ok {
					if lastXMPPErr != nil {
						return fmt.Errorf("xmpp connect aborted: %w", lastXMPPErr)
					}
					return fmt.Errorf("xmpp connect aborted")
				}

				switch e := evt.(type) {
				case events.XMPPConnected:
					// Party-service uses connection.id to validate the session; it must include the XMPP resource.
					if xmppCfg.JID != "" && xmppCfg.Resource != "" {
						c.partyCommands.SetConnectionID(xmppCfg.JID + "/" + xmppCfg.Resource)
					}
					goto xmppReady
				case events.XMPPError:
					if e.Err != nil {
						lastXMPPErr = e.Err
					}
					if e.Fatal && e.Err != nil {
						return e.Err
					}
				}
			}
		}
	}

xmppReady:

	// Match old JS behavior (fnbr.js): connect XMPP first, then init/ensure party.
	// Some party-service operations fail with "user_is_offline" until XMPP session is established.
	if _, err := c.partyCommands.EnsureParty(runCtx); err != nil {
		return err
	}

	c.emitPartyUpdated()
	_ = c.bus.Emit(events.ClientReady{Base: events.Base{At: time.Now().UTC()}})
	return nil
}

func (c *Client) loginWithDeviceAuth(ctx context.Context) error {
	if c.oauthClient == nil {
		return nil
	}

	creds, err := c.resolveDeviceAuth(ctx)
	if err != nil {
		return err
	}

	tokens, err := c.oauthClient.TokenByDeviceAuth(ctx, creds)
	if err != nil {
		return err
	}

	if err := c.tokenStore.Save(ctx, tokens); err != nil {
		return err
	}

	accountID := tokens.AccountID
	if accountID == "" {
		accountID = creds.AccountID
	}

	displayName := tokens.DisplayName
	if displayName == "" {
		displayName = c.cfg.Party.DisplayName
	}

	c.partyCommands.SetIdentity(accountID, displayName)
	c.friendsCommands.SetIdentity(accountID)
	c.friendsDecoder.SetIdentity(accountID)
	c.selfAccountID = accountID
	return nil
}

func (c *Client) resolveDeviceAuth(ctx context.Context) (auth.DeviceAuth, error) {
	creds := c.cfg.DeviceAuth
	if creds.AccountID != "" && creds.DeviceID != "" && creds.Secret != "" {
		return creds, nil
	}

	if c.deviceStore != nil && creds.AccountID != "" {
		stored, ok, err := c.deviceStore.Load(ctx, creds.AccountID)
		if err != nil {
			return auth.DeviceAuth{}, err
		}

		if ok {
			return stored, nil
		}
	}

	return auth.DeviceAuth{}, fmt.Errorf("client: device auth credentials are required")
}

func (c *Client) Logout(ctx context.Context) error {
	c.runMu.Lock()
	cancel := c.runCancel
	c.runCancel = nil
	c.runMu.Unlock()

	if cancel != nil {
		cancel()
	}

	if c.xmppClient != nil {
		if err := c.xmppClient.Close(ctx); err != nil {
			return err
		}
	}

	c.stopXMPPWatcher()
	_ = c.bus.Emit(events.ClientDisconnected{Base: events.Base{At: time.Now().UTC()}})
	return nil
}

func (c *Client) Events() <-chan events.Event {
	if c.sub == nil {
		dummy := make(chan events.Event)
		close(dummy)
		return dummy
	}

	return c.sub.C
}

func (c *Client) Subscribe(buffer int) (*events.Subscription, error) {
	return c.bus.Subscribe(buffer)
}

func (c *Client) SelfAccountID() string {
	return c.selfAccountID
}

func (c *Client) WaitFor(ctx context.Context, pred events.Predicate) (events.Event, error) {
	return c.bus.WaitFor(ctx, pred)
}

func (c *Client) PartySnapshot() *party.Party {
	return c.partyState.Snapshot()
}

func (c *Client) EnsureParty(ctx context.Context) error {
	if _, err := c.partyCommands.EnsureParty(ctx); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) JoinParty(ctx context.Context, id string) error {
	if err := c.partyCommands.JoinParty(ctx, id); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) JoinPartyByMemberID(ctx context.Context, memberAccountID string) error {
	if err := c.partyCommands.JoinPartyByMemberID(ctx, memberAccountID); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) LeaveParty(ctx context.Context) error {
	if err := c.partyCommands.LeaveParty(ctx); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) PromoteMember(ctx context.Context, accountID string) error {
	snapshot := c.partyState.Snapshot()
	if snapshot == nil || snapshot.ID == "" {
		return party.ErrNotInParty
	}

	if err := c.partyCommands.PromoteMember(ctx, snapshot.ID, accountID); err != nil {
		return err
	}

	updated := c.partyState.Snapshot()
	if updated != nil && updated.ID != "" {
		_ = c.bus.Emit(events.PartyCaptainChanged{
			Base:      events.Base{At: time.Now().UTC()},
			PartyID:   updated.ID,
			CaptainID: updated.CaptainID,
			Revision:  updated.Revision,
		})
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) AddFriend(ctx context.Context, accountID string) error {
	return c.friendsCommands.AddFriend(ctx, accountID)
}

func (c *Client) RemoveFriend(ctx context.Context, accountID string) error {
	return c.friendsCommands.RemoveFriend(ctx, accountID)
}

func (c *Client) SendJoinRequestToMember(ctx context.Context, accountID string) error {
	return c.partyCommands.SendJoinRequestToMember(ctx, accountID)
}

func (c *Client) JoinPartyInviteFrom(ctx context.Context, accountID string) (string, error) {
	return c.partyCommands.JoinPartyInviteFrom(ctx, accountID)
}

func (c *Client) ResyncParty(ctx context.Context) error {
	if err := c.partyCommands.SyncCurrentParty(ctx); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) SetReady(ctx context.Context, state party.ReadyState) error {
	if err := c.partyCommands.SetReady(ctx, state); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) SetPlaylist(ctx context.Context, req matchmaking.PlaylistRequest) error {
	if err := c.partyCommands.SetPlaylist(ctx, req); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) SetCustomKey(ctx context.Context, key string) error {
	if err := c.partyCommands.SetCustomKey(ctx, key); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) SetOutfit(ctx context.Context, outfitID string) error {
	if err := c.partyCommands.SetOutfit(ctx, outfitID); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) SetEmote(ctx context.Context, emoteID string) error {
	if err := c.partyCommands.SetEmote(ctx, emoteID); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) ClearEmote(ctx context.Context) error {
	if err := c.partyCommands.ClearEmote(ctx); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) SetLoadout(ctx context.Context, loadout party.Loadout) error {
	if err := c.partyCommands.SetLoadout(ctx, loadout); err != nil {
		return err
	}

	c.emitPartyUpdated()
	return nil
}

func (c *Client) emitPartyUpdated() {
	snapshot := c.partyState.Snapshot()
	if snapshot == nil || snapshot.ID == "" {
		return
	}

	_ = c.bus.Emit(events.PartyUpdated{
		Base:      events.Base{At: time.Now().UTC()},
		PartyID:   snapshot.ID,
		Revision:  snapshot.Revision,
		CaptainID: snapshot.CaptainID,
		Playlist:  snapshot.Playlist,
		CustomKey: snapshot.CustomKey,
	})
}

func (c *Client) startXMPPWatcher(ctx context.Context) error {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()

	if c.watchStarted {
		return nil
	}

	sub, err := c.bus.Subscribe(c.cfg.EventBuffer)
	if err != nil {
		return err
	}

	c.watchSub = sub
	c.watchStarted = true
	c.watchWG.Add(1)

	go func(eventsCh <-chan events.Event) {
		defer c.watchWG.Done()

		seenConnected := false
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-eventsCh:
				if !ok {
					return
				}

				if evt.Name() != events.EventXMPPConnected {
					continue
				}

				if !seenConnected {
					seenConnected = true
					continue
				}

				if err := c.partyCommands.ResyncAfterReconnect(ctx); err != nil {
					_ = c.bus.Emit(events.XMPPError{
						Base: events.Base{At: time.Now().UTC()},
						Err:  fmt.Errorf("party resync after reconnect: %w", err),
					})
					continue
				}

				c.emitPartyUpdated()
			}
		}
	}(sub.C)

	return nil
}

func (c *Client) stopXMPPWatcher() {
	c.watchMu.Lock()
	sub := c.watchSub
	started := c.watchStarted
	c.watchSub = nil
	c.watchStarted = false
	c.watchMu.Unlock()

	if started && sub != nil {
		sub.Cancel()
	}

	if started {
		c.watchWG.Wait()
	}
}
