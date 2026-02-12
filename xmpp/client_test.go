package xmpp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ceskypane/fngo/events"
	"github.com/gorilla/websocket"
)

type fakeTokenSource struct {
	token string
}

func (f fakeTokenSource) AccessToken(context.Context) (string, error) {
	return f.token, nil
}

type recordingStanzaDispatcher struct {
	mu      sync.Mutex
	stanzas []Stanza
}

func (d *recordingStanzaDispatcher) DispatchStanza(_ context.Context, stanza Stanza) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.stanzas = append(d.stanzas, stanza)
}

func (d *recordingStanzaDispatcher) Count() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.stanzas)
}

func TestClientHandshakeAndStanzaParsing(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		if err := expectClientFrame(conn, "<open"); err != nil {
			serverErr <- err
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte("<open xmlns='urn:ietf:params:xml:ns:xmpp-framing'/>"))

		if err := expectClientFrame(conn, "<auth"); err != nil {
			serverErr <- err
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte("<success xmlns='urn:ietf:params:xml:ns:xmpp-sasl'/>"))

		if err := expectClientFrame(conn, "<open"); err != nil {
			serverErr <- err
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte("<open xmlns='urn:ietf:params:xml:ns:xmpp-framing'/>"))

		if err := expectClientFrame(conn, "bind_1"); err != nil {
			serverErr <- err
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte("<iq id='bind_1' type='result'><bind xmlns='urn:ietf:params:xml:ns:xmpp-bind'><jid>acc@prod.ol.epicgames.com/res</jid></bind></iq>"))

		if err := expectClientFrame(conn, "sess_1"); err != nil {
			serverErr <- err
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte("<iq id='sess_1' type='result'/>"))

		_ = conn.WriteMessage(websocket.TextMessage, []byte("<presence from='friend@prod.ol.epicgames.com' type='available'/>"))
		_ = conn.WriteMessage(websocket.TextMessage, []byte("<message from='friend@prod.ol.epicgames.com' type='chat'><body>hello</body></message>"))
	}))
	defer srv.Close()

	endpoint := "ws" + strings.TrimPrefix(srv.URL, "http")
	bus := events.NewBus()
	stanzaDispatcher := &recordingStanzaDispatcher{}

	c := NewClient(Config{
		Endpoint:          endpoint,
		EnableHandshake:   true,
		HandshakeTimeout:  time.Second,
		JID:               "acc@prod.ol.epicgames.com",
		Domain:            "prod.ol.epicgames.com",
		Resource:          "res",
		MinReconnectDelay: time.Millisecond,
		MaxReconnectDelay: time.Millisecond,
		StanzaDispatcher:  stanzaDispatcher,
	}, bus, fakeTokenSource{token: "access-token"}, nil, nil)
	c.sleep = func(time.Duration) {}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if _, err := waitForEvent(bus, events.EventXMPPConnected, time.Second); err != nil {
		t.Fatalf("wait connected: %v", err)
	}

	if _, err := waitForEvent(bus, events.EventXMPPPresence, time.Second); err != nil {
		t.Fatalf("wait presence: %v", err)
	}

	msgEvent, err := waitForEvent(bus, events.EventXMPPMessage, time.Second)
	if err != nil {
		t.Fatalf("wait message: %v", err)
	}

	message, ok := msgEvent.(events.XMPPMessage)
	if !ok {
		t.Fatalf("expected XMPPMessage, got %T", msgEvent)
	}

	if message.Body != "hello" {
		t.Fatalf("unexpected message body: %s", message.Body)
	}

	if stanzaDispatcher.Count() < 2 {
		t.Fatalf("expected stanza dispatcher to receive stanzas")
	}

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server assertion failed: %v", err)
		}
	default:
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func waitForEvent(bus *events.Bus, name events.Name, timeout time.Duration) (events.Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return bus.WaitFor(ctx, func(evt events.Event) bool {
		return evt.Name() == name
	})
}

func expectClientFrame(conn *websocket.Conn, contains string) error {
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		return err
	}

	if !strings.Contains(string(payload), contains) {
		return errors.New("unexpected handshake frame")
	}

	return nil
}

type fakeDialer struct {
	mu        sync.Mutex
	attempts  int
	failUntil int
	conn      Conn
}

func (d *fakeDialer) DialContext(_ context.Context, _ string, _ http.Header) (Conn, *http.Response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.attempts++
	if d.attempts <= d.failUntil {
		return nil, nil, errors.New("dial failed")
	}

	return d.conn, nil, nil
}

func (d *fakeDialer) Attempts() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.attempts
}

type fakeConn struct {
	readCh  chan []byte
	closeCh chan struct{}
	once    sync.Once
}

func newFakeConn() *fakeConn {
	return &fakeConn{readCh: make(chan []byte, 1), closeCh: make(chan struct{})}
}

func (c *fakeConn) ReadMessage() (int, []byte, error) {
	select {
	case <-c.closeCh:
		return 0, nil, errors.New("closed")
	case payload := <-c.readCh:
		return websocket.TextMessage, payload, nil
	}
}

func (c *fakeConn) WriteMessage(_ int, _ []byte) error {
	return nil
}

func (c *fakeConn) SetReadDeadline(_ time.Time) error {
	return nil
}

func (c *fakeConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

func (c *fakeConn) Close() error {
	c.once.Do(func() {
		close(c.closeCh)
	})

	return nil
}

type fakeRecordingConn struct {
	readCh    chan []byte
	writeCh   chan []byte
	closeCh   chan struct{}
	closeOnce sync.Once
}

func newFakeRecordingConn() *fakeRecordingConn {
	return &fakeRecordingConn{
		readCh:  make(chan []byte, 1),
		writeCh: make(chan []byte, 8),
		closeCh: make(chan struct{}),
	}
}

func (c *fakeRecordingConn) ReadMessage() (int, []byte, error) {
	select {
	case <-c.closeCh:
		return 0, nil, errors.New("closed")
	case payload := <-c.readCh:
		return websocket.TextMessage, payload, nil
	}
}

func (c *fakeRecordingConn) WriteMessage(_ int, data []byte) error {
	buf := make([]byte, len(data))
	copy(buf, data)

	select {
	case <-c.closeCh:
		return errors.New("closed")
	case c.writeCh <- buf:
		return nil
	}
}

func (c *fakeRecordingConn) SetReadDeadline(_ time.Time) error {
	return nil
}

func (c *fakeRecordingConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

func (c *fakeRecordingConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closeCh)
	})

	return nil
}

func TestClientReconnectsThenConnects(t *testing.T) {
	bus := events.NewBus()
	sub, err := bus.Subscribe(64)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	conn := newFakeConn()
	dialer := &fakeDialer{failUntil: 2, conn: conn}

	c := NewClient(Config{
		Endpoint:          "wss://xmpp.example",
		EnableHandshake:   false,
		MinReconnectDelay: time.Millisecond,
		MaxReconnectDelay: time.Millisecond,
	}, bus, fakeTokenSource{token: "abc"}, nil, dialer)
	c.sleep = func(time.Duration) {}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}

	seenConnected := false
	seenReconnecting := false
	waitDeadline := time.NewTimer(time.Second)
	defer waitDeadline.Stop()

	for !(seenConnected && seenReconnecting) {
		select {
		case evt := <-sub.C:
			switch evt.Name() {
			case events.EventXMPPConnected:
				seenConnected = true
			case events.EventXMPPReconnecting:
				seenReconnecting = true
			}
		case <-waitDeadline.C:
			t.Fatalf("expected both connected and reconnecting events")
		}
	}

	if dialer.Attempts() < 3 {
		t.Fatalf("expected reconnect attempts, got %d", dialer.Attempts())
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestClientSendPingWritesXMPPStanza(t *testing.T) {
	bus := events.NewBus()
	conn := newFakeRecordingConn()
	dialer := &fakeDialer{conn: conn}

	c := NewClient(Config{
		Endpoint:          "wss://xmpp.example",
		EnableHandshake:   false,
		MinReconnectDelay: time.Millisecond,
		MaxReconnectDelay: time.Millisecond,
	}, bus, fakeTokenSource{token: "abc"}, nil, dialer)
	c.sleep = func(time.Duration) {}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if _, err := waitForEvent(bus, events.EventXMPPConnected, time.Second); err != nil {
		t.Fatalf("wait connected: %v", err)
	}

	pingID, err := c.SendPing(context.Background(), "diag_ping_1")
	if err != nil {
		t.Fatalf("send ping: %v", err)
	}

	if pingID != "diag_ping_1" {
		t.Fatalf("unexpected ping id: %s", pingID)
	}

	select {
	case payload := <-conn.writeCh:
		text := string(payload)
		if !strings.Contains(text, "id='diag_ping_1'") {
			t.Fatalf("ping stanza missing id: %s", text)
		}

		if !strings.Contains(text, "urn:xmpp:ping") {
			t.Fatalf("ping stanza missing namespace: %s", text)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for ping stanza write")
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestBuildPingStanzaAutoID(t *testing.T) {
	pingID, stanza := buildPingStanza("")
	if pingID == "" {
		t.Fatalf("expected generated ping id")
	}

	if !strings.Contains(stanza, "type='get'") {
		t.Fatalf("expected iq get stanza: %s", stanza)
	}

	if !strings.Contains(stanza, "urn:xmpp:ping") {
		t.Fatalf("expected ping namespace: %s", stanza)
	}

	if !strings.Contains(stanza, "id='"+pingID+"'") {
		t.Fatalf("expected stanza id to match generated id: %s", stanza)
	}
}

func TestClientReconnectBudgetExceededEmitsFatalError(t *testing.T) {
	bus := events.NewBus()
	dialer := &fakeDialer{failUntil: 100}

	c := NewClient(Config{
		Endpoint:             "wss://xmpp.example",
		EnableHandshake:      false,
		MinReconnectDelay:    time.Millisecond,
		MaxReconnectDelay:    time.Millisecond,
		MaxReconnectAttempts: 2,
	}, bus, fakeTokenSource{token: "abc"}, nil, dialer)
	c.sleep = func(time.Duration) {}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()

	evt, err := bus.WaitFor(waitCtx, func(evt events.Event) bool {
		xmppErr, ok := evt.(events.XMPPError)
		if !ok {
			return false
		}

		return xmppErr.Fatal
	})
	if err != nil {
		t.Fatalf("wait fatal xmpp error: %v", err)
	}

	xmppErr := evt.(events.XMPPError)
	if !errors.Is(xmppErr.Err, ErrReconnectBudgetExceeded) {
		t.Fatalf("expected reconnect budget error, got %v", xmppErr.Err)
	}

	if dialer.Attempts() < 3 {
		t.Fatalf("expected at least 3 attempts (initial + retries), got %d", dialer.Attempts())
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}
