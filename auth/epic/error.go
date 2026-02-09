package epic

import (
	"fmt"
	"strings"
)

type OAuthError struct {
	StatusCode       int
	ErrorCode        string
	OAuthError       string
	ErrorDescription string
	Message          string
	NumericCode      int
	MessageVars      []string
}

func (e *OAuthError) Error() string {
	parts := make([]string, 0, 4)
	if e.ErrorCode != "" {
		parts = append(parts, e.ErrorCode)
	}
	if e.OAuthError != "" {
		parts = append(parts, e.OAuthError)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	if e.ErrorDescription != "" {
		parts = append(parts, e.ErrorDescription)
	}

	if len(parts) == 0 {
		return fmt.Sprintf("epic oauth error (status=%d)", e.StatusCode)
	}

	return fmt.Sprintf("epic oauth error (status=%d): %s", e.StatusCode, strings.Join(parts, " | "))
}

func (e *OAuthError) Retryable() bool {
	if e == nil {
		return false
	}

	if e.StatusCode == 429 {
		return true
	}

	if e.StatusCode >= 500 && e.StatusCode <= 599 {
		return true
	}

	return false
}

func (e *OAuthError) Fatal() bool {
	if e == nil {
		return false
	}

	oauthErr := strings.ToLower(e.OAuthError)
	if oauthErr == "invalid_grant" {
		return true
	}

	code := strings.ToLower(e.ErrorCode)
	if strings.Contains(code, "invalid_grant") {
		return true
	}

	return false
}

func IsInvalidGrant(err error) bool {
	oauthErr, ok := err.(*OAuthError)
	if !ok {
		return false
	}

	return oauthErr.Fatal()
}
