package auth

import "time"

type DeviceAuth struct {
	AccountID string
	DeviceID  string
	Secret    string
}

type TokenSet struct {
	AccessToken      string
	RefreshToken     string
	TokenType        string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
	AccountID        string
	ClientID         string
	DisplayName      string
}

func (t TokenSet) Clone() TokenSet {
	return TokenSet{
		AccessToken:      t.AccessToken,
		RefreshToken:     t.RefreshToken,
		TokenType:        t.TokenType,
		ExpiresAt:        t.ExpiresAt,
		RefreshExpiresAt: t.RefreshExpiresAt,
		AccountID:        t.AccountID,
		ClientID:         t.ClientID,
		DisplayName:      t.DisplayName,
	}
}
