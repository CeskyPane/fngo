package xmpp

import (
	"context"
	"net/http"
	"time"

	"github.com/ceskypane/fngo/logging"
)

type TokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

type Frame struct {
	MessageType int
	Payload     string
}

type Dispatcher interface {
	Dispatch(ctx context.Context, frame Frame)
}

type StanzaKind string

const (
	StanzaKindMessage  StanzaKind = "message"
	StanzaKindPresence StanzaKind = "presence"
	StanzaKindIQ       StanzaKind = "iq"
	StanzaKindRaw      StanzaKind = "raw"
)

type Stanza struct {
	Kind     StanzaKind
	Name     string
	ID       string
	From     string
	To       string
	Type     string
	Body     string
	InnerXML string
	Raw      string
}

type StanzaDispatcher interface {
	DispatchStanza(ctx context.Context, stanza Stanza)
}

type Conn interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(messageType int, data []byte) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

type Dialer interface {
	DialContext(ctx context.Context, endpoint string, header http.Header) (Conn, *http.Response, error)
}

type Config struct {
	Endpoint             string
	WriteBuffer          int
	MinReconnectDelay    time.Duration
	MaxReconnectDelay    time.Duration
	MaxReconnectAttempts int
	ReadDeadline         time.Duration
	WriteDeadline        time.Duration
	KeepAliveInterval    time.Duration

	EnableHandshake  bool
	HandshakeTimeout time.Duration
	JID              string
	Username         string
	Domain           string
	Resource         string

	StanzaDispatcher StanzaDispatcher
	Logger           logging.Logger
}
