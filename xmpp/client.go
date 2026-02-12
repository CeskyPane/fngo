package xmpp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/internal/backoff"
	"github.com/ceskypane/fngo/logging"
	"github.com/gorilla/websocket"
)

var (
	ErrNotRunning              = errors.New("xmpp: client not running")
	ErrReconnectBudgetExceeded = errors.New("xmpp: reconnect attempts exhausted")
)

type Client struct {
	cfg              Config
	bus              *events.Bus
	tokenSource      TokenSource
	frameDispatcher  Dispatcher
	stanzaDispatcher StanzaDispatcher
	dialer           Dialer
	sleep            func(time.Duration)
	logger           logging.Logger

	sendCh chan []byte

	mu      sync.Mutex
	conn    Conn
	running bool
	cancel  context.CancelFunc

	wg sync.WaitGroup
}

func NewClient(cfg Config, bus *events.Bus, tokenSource TokenSource, dispatcher Dispatcher, dialer Dialer) *Client {
	if bus == nil {
		bus = events.NewBus()
	}

	if cfg.WriteBuffer <= 0 {
		cfg.WriteBuffer = 64
	}

	if cfg.MinReconnectDelay <= 0 {
		cfg.MinReconnectDelay = 200 * time.Millisecond
	}

	if cfg.MaxReconnectDelay <= 0 {
		cfg.MaxReconnectDelay = 3 * time.Second
	}

	if cfg.MaxReconnectDelay < cfg.MinReconnectDelay {
		cfg.MaxReconnectDelay = cfg.MinReconnectDelay
	}

	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 8 * time.Second
	}

	if cfg.Resource == "" {
		cfg.Resource = "V2:Fortnite:WIN::FNGO"
	}

	if dialer == nil {
		dialer = &gorillaDialer{dialer: websocket.DefaultDialer}
	}

	logger := logging.With(cfg.Logger)

	return &Client{
		cfg:              cfg,
		bus:              bus,
		tokenSource:      tokenSource,
		frameDispatcher:  dispatcher,
		stanzaDispatcher: cfg.StanzaDispatcher,
		dialer:           dialer,
		sleep:            time.Sleep,
		logger:           logger,
		sendCh:           make(chan []byte, cfg.WriteBuffer),
	}
}

func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.running = true

	c.wg.Add(1)
	go c.supervisor(runCtx)
	c.logger.Info("xmpp connect supervisor started", logging.F("endpoint", c.cfg.Endpoint))

	return nil
}

func (c *Client) Close(ctx context.Context) error {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}

	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	c.logger.Info("xmpp close requested")

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (c *Client) SendRaw(ctx context.Context, raw string) error {
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()

	if !running {
		return ErrNotRunning
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.sendCh <- []byte(raw):
		return nil
	}
}

func (c *Client) SendPing(ctx context.Context, id string) (string, error) {
	pingID, stanza := buildPingStanza(id)
	if err := c.SendRaw(ctx, stanza); err != nil {
		return "", err
	}

	return pingID, nil
}

func buildPingStanza(id string) (string, string) {
	pingID := id
	if pingID == "" {
		pingID = fmt.Sprintf("ping_%d", time.Now().UTC().UnixNano())
	}

	raw := "<iq type='get' id='" + xmlEscape(pingID) + "'><ping xmlns='urn:xmpp:ping'/></iq>"
	return pingID, raw
}

func (c *Client) supervisor(ctx context.Context) {
	defer c.wg.Done()
	defer c.markStopped()

	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return
		}

		err := c.connectAndRun(ctx)
		if err == nil {
			failures = 0
			continue
		}

		if errors.Is(err, context.Canceled) {
			return
		}

		failures++
		delay := backoff.Exponential(failures-1, c.cfg.MinReconnectDelay, c.cfg.MaxReconnectDelay)

		c.logger.Warn("xmpp disconnected, scheduling reconnect",
			logging.F("attempt", failures),
			logging.F("delay", delay.String()),
			logging.F("error", err.Error()),
		)

		_ = c.bus.Emit(events.XMPPReconnecting{
			Base:    events.Base{At: time.Now().UTC()},
			Attempt: failures,
			Delay:   delay,
			Err:     err,
		})

		if c.cfg.MaxReconnectAttempts > 0 && failures > c.cfg.MaxReconnectAttempts {
			reconnectErr := fmt.Errorf("%w: attempts=%d last_error=%v", ErrReconnectBudgetExceeded, failures-1, err)
			c.logger.Error("xmpp reconnect budget exhausted",
				logging.F("attempts", failures-1),
				logging.F("error", err.Error()),
			)
			_ = c.bus.Emit(events.XMPPError{Base: events.Base{At: time.Now().UTC()}, Err: reconnectErr, Fatal: true})
			return
		}

		_ = c.bus.Emit(events.XMPPError{Base: events.Base{At: time.Now().UTC()}, Err: err})

		if !c.sleepContext(ctx, delay) {
			return
		}
	}
}

func (c *Client) connectAndRun(ctx context.Context) error {
	headers := http.Header{}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}

	conn, _, err := c.dialer.DialContext(ctx, c.cfg.Endpoint, headers)
	if err != nil {
		return err
	}

	c.logger.Info("xmpp transport connected", logging.F("endpoint", c.cfg.Endpoint))

	if c.cfg.EnableHandshake {
		if err := c.performHandshake(ctx, conn, token); err != nil {
			_ = conn.Close()
			return err
		}

		// Send initial presence to mark the session as online/available.
		// Without this, party-service operations may fail with "user_is_offline".
		if err := c.writeHandshakeFrame(conn, "<presence/>"); err != nil {
			_ = conn.Close()
			return err
		}
	}

	c.setConn(conn)
	_ = c.bus.Emit(events.XMPPConnected{Base: events.Base{At: time.Now().UTC()}, Endpoint: c.cfg.Endpoint})

	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 3)
	go c.readLoop(loopCtx, conn, errCh)
	go c.writeLoop(loopCtx, conn, errCh)
	if c.cfg.KeepAliveInterval > 0 {
		go c.keepAliveLoop(loopCtx, errCh)
	}

	err = <-errCh
	_ = conn.Close()
	c.clearConn(conn)
	_ = c.bus.Emit(events.XMPPDisconnected{Base: events.Base{At: time.Now().UTC()}, Err: err})
	if err != nil {
		c.logger.Warn("xmpp transport disconnected", logging.F("error", err.Error()))
	}

	return err
}

func (c *Client) performHandshake(ctx context.Context, conn Conn, accessToken string) error {
	domain := c.cfg.Domain
	if domain == "" {
		domain = domainFromJID(c.cfg.JID)
	}
	if domain == "" {
		domain = "prod.ol.epicgames.com"
	}

	username := c.cfg.Username
	if username == "" {
		username = usernameFromJID(c.cfg.JID)
	}

	if username == "" {
		return fmt.Errorf("xmpp: username is required for handshake")
	}

	if accessToken == "" {
		return fmt.Errorf("xmpp: access token is required for handshake")
	}

	if err := c.writeHandshakeFrame(conn, fmt.Sprintf("<open to='%s' version='1.0' xmlns='urn:ietf:params:xml:ns:xmpp-framing'/>", xmlEscape(domain))); err != nil {
		return err
	}

	if _, err := c.waitForHandshakeFrame(ctx, conn, func(frame string) bool {
		return strings.Contains(frame, "<open") || strings.Contains(frame, "<features")
	}); err != nil {
		return err
	}

	plain := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + accessToken))
	if err := c.writeHandshakeFrame(conn, "<auth xmlns='urn:ietf:params:xml:ns:xmpp-sasl' mechanism='PLAIN'>"+plain+"</auth>"); err != nil {
		return err
	}

	if _, err := c.waitForHandshakeFrame(ctx, conn, func(frame string) bool {
		return strings.Contains(frame, "<success")
	}); err != nil {
		return err
	}

	if err := c.writeHandshakeFrame(conn, fmt.Sprintf("<open to='%s' version='1.0' xmlns='urn:ietf:params:xml:ns:xmpp-framing'/>", xmlEscape(domain))); err != nil {
		return err
	}

	if _, err := c.waitForHandshakeFrame(ctx, conn, func(frame string) bool {
		return strings.Contains(frame, "<open") || strings.Contains(frame, "<features")
	}); err != nil {
		return err
	}

	bind := "<iq type='set' id='bind_1'><bind xmlns='urn:ietf:params:xml:ns:xmpp-bind'><resource>" + xmlEscape(c.cfg.Resource) + "</resource></bind></iq>"
	if err := c.writeHandshakeFrame(conn, bind); err != nil {
		return err
	}

	if _, err := c.waitForHandshakeFrame(ctx, conn, func(frame string) bool {
		return strings.Contains(frame, "bind_1") && strings.Contains(frame, "type='result'") || strings.Contains(frame, "bind_1") && strings.Contains(frame, "type=\"result\"")
	}); err != nil {
		return err
	}

	session := "<iq to='" + xmlEscape(domain) + "' type='set' id='sess_1'><session xmlns='urn:ietf:params:xml:ns:xmpp-session'/></iq>"
	if err := c.writeHandshakeFrame(conn, session); err != nil {
		return err
	}

	if _, err := c.waitForHandshakeFrame(ctx, conn, func(frame string) bool {
		return strings.Contains(frame, "sess_1") && strings.Contains(frame, "type='result'") || strings.Contains(frame, "sess_1") && strings.Contains(frame, "type=\"result\"")
	}); err != nil {
		return err
	}

	return nil
}

func (c *Client) waitForHandshakeFrame(ctx context.Context, conn Conn, match func(string) bool) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		if c.cfg.HandshakeTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(c.cfg.HandshakeTimeout))
		}

		_, payload, err := conn.ReadMessage()
		if err != nil {
			return "", err
		}

		frame := string(payload)
		if match(frame) {
			return frame, nil
		}
	}
}

func (c *Client) writeHandshakeFrame(conn Conn, frame string) error {
	if c.cfg.WriteDeadline > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteDeadline))
	}

	return conn.WriteMessage(websocket.TextMessage, []byte(frame))
}

func domainFromJID(jid string) string {
	parts := strings.SplitN(jid, "@", 2)
	if len(parts) != 2 {
		return ""
	}

	return parts[1]
}

func usernameFromJID(jid string) string {
	parts := strings.SplitN(jid, "@", 2)
	if len(parts) == 0 {
		return ""
	}

	return parts[0]
}

func xmlEscape(v string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)

	return replacer.Replace(v)
}

func (c *Client) readLoop(ctx context.Context, conn Conn, errCh chan<- error) {
	for {
		if c.cfg.ReadDeadline > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadDeadline))
		}

		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}

		frame := Frame{MessageType: messageType, Payload: string(payload)}
		if c.frameDispatcher != nil {
			c.frameDispatcher.Dispatch(ctx, frame)
		}

		now := time.Now().UTC()
		_ = c.bus.Emit(events.XMPPFrame{Base: events.Base{At: now}, MessageType: frame.MessageType, Payload: frame.Payload})

		stanza, parseErr := ParseStanza(frame.Payload)
		if parseErr != nil {
			_ = c.bus.Emit(events.XMPPRaw{Base: events.Base{At: now}, Raw: frame.Payload, Err: parseErr})
			continue
		}

		if c.stanzaDispatcher != nil {
			c.stanzaDispatcher.DispatchStanza(ctx, stanza)
		}

		switch stanza.Kind {
		case StanzaKindMessage:
			_ = c.bus.Emit(events.XMPPMessage{Base: events.Base{At: now}, ID: stanza.ID, From: stanza.From, To: stanza.To, Type: stanza.Type, Body: stanza.Body, Raw: stanza.Raw})
		case StanzaKindPresence:
			_ = c.bus.Emit(events.XMPPPresence{Base: events.Base{At: now}, ID: stanza.ID, From: stanza.From, To: stanza.To, Type: stanza.Type, Raw: stanza.Raw})
		case StanzaKindIQ:
			_ = c.bus.Emit(events.XMPPIQ{Base: events.Base{At: now}, ID: stanza.ID, From: stanza.From, To: stanza.To, Type: stanza.Type, Raw: stanza.Raw})
		default:
			_ = c.bus.Emit(events.XMPPRaw{Base: events.Base{At: now}, Raw: stanza.Raw})
		}
	}
}

func (c *Client) writeLoop(ctx context.Context, conn Conn, errCh chan<- error) {
	for {
		select {
		case <-ctx.Done():
			errCh <- ctx.Err()
			return
		case msg := <-c.sendCh:
			if c.cfg.WriteDeadline > 0 {
				_ = conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteDeadline))
			}

			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				errCh <- err
				return
			}
		}
	}
}

func (c *Client) keepAliveLoop(ctx context.Context, errCh chan<- error) {
	t := time.NewTicker(c.cfg.KeepAliveInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			select {
			case <-ctx.Done():
				return
			case c.sendCh <- []byte(" "):
			default:
				errCh <- fmt.Errorf("xmpp: keepalive queue is full")
				return
			}
		}
	}
}

func (c *Client) setConn(conn Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.conn = conn
}

func (c *Client) clearConn(conn Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == conn {
		c.conn = nil
	}
}

func (c *Client) markStopped() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.running = false
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}

	c.logger.Info("xmpp supervisor stopped")
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	if c.tokenSource == nil {
		return "", nil
	}

	token, err := c.tokenSource.AccessToken(ctx)
	if err != nil {
		return "", err
	}

	return token, nil
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

type gorillaDialer struct {
	dialer *websocket.Dialer
}

func (d *gorillaDialer) DialContext(ctx context.Context, endpoint string, header http.Header) (Conn, *http.Response, error) {
	c, resp, err := d.dialer.DialContext(ctx, endpoint, header)
	if err != nil {
		return nil, resp, err
	}

	return &gorillaConn{conn: c}, resp, nil
}

type gorillaConn struct {
	conn *websocket.Conn
}

func (c *gorillaConn) ReadMessage() (int, []byte, error) {
	return c.conn.ReadMessage()
}

func (c *gorillaConn) WriteMessage(messageType int, data []byte) error {
	return c.conn.WriteMessage(messageType, data)
}

func (c *gorillaConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *gorillaConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (c *gorillaConn) Close() error {
	return c.conn.Close()
}
