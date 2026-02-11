package client

import (
	"net/http"

	"github.com/ceskypane/fngo/auth"
	"github.com/ceskypane/fngo/auth/epic"
	"github.com/ceskypane/fngo/friends"
	"github.com/ceskypane/fngo/logging"
	"github.com/ceskypane/fngo/party"
	transporthttp "github.com/ceskypane/fngo/transport/http"
	"github.com/ceskypane/fngo/xmpp"
)

type Config struct {
	EventBuffer int
	Logger      logging.Logger

	DeviceAuth      auth.DeviceAuth
	DeviceAuthStore auth.DeviceAuthStore
	TokenStore      auth.TokenStore

	OAuth epic.Config

	AuthScheduler *auth.SchedulerConfig

	HTTPClient *http.Client
	HTTP       transporthttp.Config

	// DisableFortniteInit disables the best-effort EULA/grant_access calls during Login.
	// It is primarily intended for offline/unit tests.
	DisableFortniteInit bool

	Party party.Config

	Friends friends.Config

	EnableXMPP           bool
	XMPP                 xmpp.Config
	XMPPDispatcher       xmpp.Dispatcher
	XMPPStanzaDispatcher xmpp.StanzaDispatcher
	XMPPDialer           xmpp.Dialer
}
