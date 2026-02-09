package epic

import (
	"context"
	"net/http"
	"time"

	"github.com/ceskypane/fngo/auth"
)

const DefaultTokenURL = "https://account-public-service-prod.ol.epicgames.com/account/api/oauth/token"

type ExchangeCodeGrant struct {
	ExchangeCode string
}

type AuthorizationCodeGrant struct {
	Code string
}

type Config struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client

	MaxRetries int
	MinBackoff time.Duration
	MaxBackoff time.Duration
}

type EpicOAuthClient interface {
	TokenByDeviceAuth(ctx context.Context, creds auth.DeviceAuth) (auth.TokenSet, error)
	TokenByRefresh(ctx context.Context, refreshToken string) (auth.TokenSet, error)
	TokenByExchangeCode(ctx context.Context, grant ExchangeCodeGrant) (auth.TokenSet, error)
	TokenByAuthorizationCode(ctx context.Context, grant AuthorizationCodeGrant) (auth.TokenSet, error)
}
