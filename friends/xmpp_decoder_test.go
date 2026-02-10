package friends

import (
	"context"
	"testing"
	"time"

	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/xmpp"
)

func TestXMPPDecoderEmitsFriendAdded(t *testing.T) {
	bus := events.NewBus()
	sub, err := bus.Subscribe(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	dec := NewXMPPDecoder(bus)
	dec.SetIdentity("self")

	dec.DispatchStanza(context.Background(), xmpp.Stanza{
		Kind: xmpp.StanzaKindMessage,
		Body: `{"type":"com.epicgames.friends.core.apiobjects.Friend","payload":{"status":"ACCEPTED","accountId":"p1"}}`,
	})

	assertEvent(t, sub.C, func(evt events.Event) bool {
		added, ok := evt.(events.FriendAdded)
		return ok && added.AccountID == "p1"
	})
}

func TestXMPPDecoderEmitsFriendRequestReceived(t *testing.T) {
	bus := events.NewBus()
	sub, err := bus.Subscribe(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	dec := NewXMPPDecoder(bus)
	dec.SetIdentity("self")

	dec.DispatchStanza(context.Background(), xmpp.Stanza{
		Kind: xmpp.StanzaKindMessage,
		Body: `{"type":"com.epicgames.friends.core.apiobjects.Friend","payload":{"status":"PENDING","direction":"INBOUND","accountId":"p2"}}`,
	})

	assertEvent(t, sub.C, func(evt events.Event) bool {
		req, ok := evt.(events.FriendRequestReceived)
		return ok && req.AccountID == "p2"
	})
}

func TestXMPPDecoderEmitsFriendRequestDeclined(t *testing.T) {
	bus := events.NewBus()
	sub, err := bus.Subscribe(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	dec := NewXMPPDecoder(bus)
	dec.SetIdentity("self")

	dec.DispatchStanza(context.Background(), xmpp.Stanza{
		Kind: xmpp.StanzaKindMessage,
		Body: `{"type":"FRIENDSHIP_REMOVE","from":"self","to":"p3","reason":"REJECTED"}`,
	})

	assertEvent(t, sub.C, func(evt events.Event) bool {
		declined, ok := evt.(events.FriendRequestDeclined)
		return ok && declined.AccountID == "p3"
	})
}

func assertEvent(t *testing.T, ch <-chan events.Event, predicate func(events.Event) bool) {
	t.Helper()

	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed")
			}

			if predicate(evt) {
				return
			}
		case <-timer.C:
			t.Fatalf("timeout waiting for event")
		}
	}
}
