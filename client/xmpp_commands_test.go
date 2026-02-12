package client

import (
	"context"
	"strings"
	"testing"
)

func TestSendXMPPRawWithoutXMPPClientReturnsError(t *testing.T) {
	c := &Client{}
	err := c.SendXMPPRaw(context.Background(), "<presence/>")
	if err == nil {
		t.Fatalf("expected error when xmpp client is unavailable")
	}

	if !strings.Contains(err.Error(), "xmpp client unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendXMPPPingWithoutXMPPClientReturnsError(t *testing.T) {
	c := &Client{}
	_, err := c.SendXMPPPing(context.Background(), "diag_ping_1")
	if err == nil {
		t.Fatalf("expected error when xmpp client is unavailable")
	}

	if !strings.Contains(err.Error(), "xmpp client unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}
