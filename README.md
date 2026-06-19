# herrscher-discord-gateway

**The Discord channel edge.** This module teaches the Herrscher platform to speak
Discord. It is a **pure plugin** — no `main`, no composition root. It adapts the
low-level [`dctl`](https://github.com/Herrscherd/dctl) Discord client to the
[`herrscher-contracts`](https://github.com/Herrscherd/herrscher-contracts) ports, and
self-registers into the global plugin registry from its `init()` (xcaddy pattern), so
the host enables Discord with a blank import + rebuild — no wiring.

```
require (
    github.com/Herrscherd/dctl                 // Discord REST client + slash builders
    github.com/Herrscherd/herrscher-contracts  // the ports it satisfies
    github.com/coder/websocket                 // gateway websocket transport
)
```

---

## What it provides

`NewGatewaySet` (the registered factory) builds, from runtime config (`token`,
`channel`), every platform surface the daemon binds into a `contracts.GatewaySet`:

| Constructor | Satisfies | Role |
|-------------|-----------|------|
| `NewGateway(c)` | `contracts.Gateway` | post / reply / react / menu, plus `Manifest()`; also drives the slash surface |
| `NewPlatform(c)` | `contracts.ChannelReader` | read history, ensure channels, upsert the status message |
| `NewChannelAdmin(c)` | `contracts.ChannelAdmin` | create-under / forum-post / archive / send / kind |
| `NewProber(c)` | `contracts.Prober` | cheap `/users/@me` round-trip for health latency |

The host wraps the Gateway in `contracts.Degrade(...)` so the core can always call the
richest method; degradation also forwards `BindSessionControl` so the slash surface is
reached even through the wrapper.

---

## The slash surface (entirely plugin-side)

All slash handling lives here — the core never learns the Discord command surface.
`slash.go` declares the command catalog with the `dctl` builders, `ws.go` receives the
interactions, and the plugin translates each one into either:

- a **neutral argv** dispatched through `contracts.SessionControl.Dispatch` (the seam
  the host binds via `BindSessionControl`), for the commands the core owns; or
- a mutation of the **plugin-local allow store** (`allow.go`), for the permission
  lists the core never sees.

| Command | Subcommands / options | Goes to |
|---------|-----------------------|---------|
| `/set` | `home <channel>`, `source <path>` | core (argv) |
| `/session` | `create [name] [cmd] [shared] [backend: stream\|oneshot] [project] [clone]`, `close [name] [force]`, `list`, `who [name]` | core (argv) |
| `/session allow` | `add <name> <user>`, `remove <name> <user>`, `list <name>` | allow store |
| `/service` | `restart`, `update [no_pull]` | core (argv) |
| `/allow` | `add <user>`, `remove <user>`, `list` | allow store |

Session-name options (`close`/`who`/`session allow …`) autocomplete from
`SessionControl.Sessions()`.

### Permissions

Two gates stack. Each command is published with `default_member_permissions =
Manage Server`, so Discord only shows it to server managers. The plugin-local allow
store (`/allow`) is the finer per-user gate: an **empty** global list allows everyone
who can see the command (so the first operator can bootstrap), and once populated only
listed users may run commands or autocomplete. The store persists as JSON
(`discord-allow.json`, mode 0600) beside the daemon state (`DCTL_STATE_DIR`, else
`~/.config/dctl`). `default_member_permissions` is a UI default a guild admin can
override, so treat the allow list as the real policy and populate it.

---

## The gateway websocket

`ws.go` is a Discord Gateway v10 client. Interactions are delivered over the gateway
regardless of intents, so it identifies with `intents=0` and only acts on
`INTERACTION_CREATE`. It heartbeats on the server-supplied interval, tracks heartbeat
ACKs to detect a half-dead connection (forcing a reconnect when a beat goes unACKed),
and reconnects with exponential backoff until the daemon context is cancelled. It runs
once the host binds the session controller (`BindSessionControl`).

---

## Select-menu choice routing

When the core posts a select menu, the `custom_id` carries the **conversation id**, so
a click routes back to the right conversation. `choice.go` provides the codec:

```go
func ChoiceCustomID(conv string) string             // prefix + conv id
func ParseChoiceCustomID(id string) (string, bool)   // extract the conv id
```

---

## Layout

| File | Contents |
|------|----------|
| `register.go` | `init()` self-registration + `NewGatewaySet` factory + allow-store path |
| `gateway.go` | `Gateway` adapter, `Manifest`, `BindSessionControl` |
| `slash.go` | slash catalog, interaction→argv translation, allow-list handlers, autocomplete |
| `ws.go` | Discord Gateway v10 websocket client (identify / heartbeat / reconnect) |
| `allow.go` | plugin-local permission store (global + per-session) |
| `adapters.go` | `ChannelAdmin`, `Platform`, `Prober` |
| `choice.go` | select-menu `custom_id` codec |

---

## Build & test

```bash
go build ./...
go vet ./...
go test ./...
```

Go 1.25. Depends on `dctl`, `herrscher-contracts`, and `coder/websocket`. Pure plugin —
no binary. The host is the only thing that constructs these adapters (blank import).
