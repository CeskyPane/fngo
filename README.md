# fngo

Unofficial Fortnite protocol client in Go: OAuth, token-aware HTTP, XMPP over WebSocket, and a production-oriented party layer with typed events.

This project is not affiliated with Epic Games. Use responsibly and at your own risk.

## Features
- Epic OAuth (device auth + refresh token) with a refresh scheduler
- HTTP transport with retry/backoff, 429 `Retry-After`, and refresh-on-auth-failure
- XMPP-over-WebSocket scaffold with reconnect loop and basic stanza decoding (`message`/`presence`/`iq`)
- Party HTTP operations (ensure/create/join/leave/patch) with revision-safe patch retry
- Typed events + `WaitFor(ctx, predicate)` with timeout/cancel and no goroutine leaks

## Install
```bash
go get github.com/ceskypane/fngo@latest
```

## Quickstart
```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/ceskypane/fngo/auth"
	"github.com/ceskypane/fngo/auth/epic"
	"github.com/ceskypane/fngo/client"
	"github.com/ceskypane/fngo/events"
	"github.com/ceskypane/fngo/matchmaking"
	"github.com/ceskypane/fngo/party"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := client.NewClient(client.Config{
		OAuth: epic.Config{
			ClientID:     "<epic_oauth_client_id>",
			ClientSecret: "<epic_oauth_client_secret>",
		},
		DeviceAuth: auth.DeviceAuth{
			AccountID: "<account_id>",
			DeviceID:  "<device_id>",
			Secret:    "<device_secret>",
		},
		EnableXMPP: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := c.Login(ctx); err != nil {
		log.Fatal(err)
	}
	defer c.Logout(context.Background())

	// Always start in a valid party (create if none exists).
	if err := c.EnsureParty(ctx); err != nil {
		log.Fatal(err)
	}

	// Apply some common intents.
	if err := c.SetPlaylist(ctx, matchmaking.PlaylistRequest{
		PlaylistID: "Playlist_DefaultSolo",
		Region:     "NAE",
		TeamFill:   false,
	}); err != nil {
		log.Fatal(err)
	}

	if err := c.SetCustomKey(ctx, "my-custom-key"); err != nil {
		log.Fatal(err)
	}

	if err := c.SetReady(ctx, party.ReadyStateReady); err != nil {
		log.Fatal(err)
	}

	// Wait for a deterministic confirmation via typed events.
	waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
	defer waitCancel()

	if _, err := c.WaitFor(waitCtx, events.PartyCustomKeyEquals("", "my-custom-key")); err != nil {
		log.Fatal(err)
	}
}
```

## Events
`Client.Events()` returns a channel of typed events (best-effort delivery, non-blocking). For strict gating, prefer `WaitFor`:

```go
evt, err := c.WaitFor(ctx, events.PartyPlaylistEquals("", "Playlist_DefaultSolo"))
_ = evt
_ = err
```

## Package Overview
- `client`: high-level facade (login, XMPP wiring, party commands)
- `auth`, `auth/epic`: token models + Epic OAuth grants + refresh scheduler
- `transport/http`: token-aware HTTP client with retries/backoff
- `xmpp`: WebSocket transport, reconnect loop, minimal stanza decoding
- `party`: party state, reducer, HTTP ops, patch builder, XMPP notification decoding
- `events`: typed event bus with `Subscribe` + `WaitFor`

## Development
```bash
gofmt -w .
go test ./...
```
