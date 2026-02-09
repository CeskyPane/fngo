package xmpp

import (
	"encoding/xml"
	"errors"
	"strings"
)

var ErrInvalidStanza = errors.New("xmpp: invalid stanza")

type stanzaEnvelope struct {
	XMLName  xml.Name
	ID       string `xml:"id,attr"`
	From     string `xml:"from,attr"`
	To       string `xml:"to,attr"`
	Type     string `xml:"type,attr"`
	Body     string `xml:"body"`
	InnerXML string `xml:",innerxml"`
}

func ParseStanza(raw string) (Stanza, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Stanza{Kind: StanzaKindRaw, Raw: raw}, ErrInvalidStanza
	}

	decoder := xml.NewDecoder(strings.NewReader(trimmed))
	for {
		tok, err := decoder.Token()
		if err != nil {
			return Stanza{Kind: StanzaKindRaw, Raw: raw}, err
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		var env stanzaEnvelope
		if err := decoder.DecodeElement(&env, &start); err != nil {
			return Stanza{Kind: StanzaKindRaw, Raw: raw}, err
		}

		stanza := Stanza{
			Kind:     StanzaKindRaw,
			Name:     start.Name.Local,
			ID:       env.ID,
			From:     env.From,
			To:       env.To,
			Type:     env.Type,
			Body:     strings.TrimSpace(env.Body),
			InnerXML: env.InnerXML,
			Raw:      trimmed,
		}

		switch start.Name.Local {
		case "message":
			stanza.Kind = StanzaKindMessage
		case "presence":
			stanza.Kind = StanzaKindPresence
		case "iq":
			stanza.Kind = StanzaKindIQ
		default:
			stanza.Kind = StanzaKindRaw
		}

		return stanza, nil
	}
}
