package friends

import "github.com/ceskypane/fngo/logging"

type Config struct {
	// BaseURL should be the Epic friends service origin, e.g.
	// https://friends-public-service-prod.ol.epicgames.com
	BaseURL string

	// AccountID is the bot's Epic account id (self).
	AccountID string

	Logger logging.Logger
}

func DefaultConfig() Config {
	return Config{
		BaseURL: "https://friends-public-service-prod.ol.epicgames.com",
	}
}
