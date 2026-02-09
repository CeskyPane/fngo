package client

import (
	"context"

	"github.com/ceskypane/fngo/xmpp"
)

type chainStanzaDispatcher struct {
	items []xmpp.StanzaDispatcher
}

func (d *chainStanzaDispatcher) DispatchStanza(ctx context.Context, stanza xmpp.Stanza) {
	for _, item := range d.items {
		if item == nil {
			continue
		}

		item.DispatchStanza(ctx, stanza)
	}
}
