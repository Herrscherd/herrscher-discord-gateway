# herrscher-discord-gateway

**The channel edge.** This module teaches the Herrscher platform to speak Discord.
It is a **pure library** — it has no `main` and no composition root. It adapts the
low-level [`dctl`](../dctl) Discord client to the
[`herrscher-contracts`](../herrscher-contracts/README.md) ports, so the core can post
messages, read history, register slash commands and stream interactions without ever
importing anything Discord-specific.

> Part of the [Herrscher](../herrscher-host/README.md) family:
> [contracts](../herrscher-contracts/README.md) ·
> [core](../herrscher-core/README.md) ·
> [claude-backend](../herrscher-claude-backend/README.md) ·
> **discord-gateway** · [host](../herrscher-host/README.md)

```
require (
    github.com/Herrscherd/dctl                  // low-level Discord REST/WS client
    github.com/Herrscherd/herrscher-contracts   // the ports it satisfies
    github.com/coder/websocket                 // gateway websocket transport
)
```

---

## What it adapts

The daemon needs many small platform surfaces, not one giant client. Each
constructor here builds an adapter that satisfies one (or more) contracts port:

| Constructor | Satisfies | Role |
|-------------|-----------|------|
| `NewGateway(c)` | `contracts.Gateway` | post / reply / react / menu, plus `Manifest()` |
| `NewPlatform(c)` | `contracts.Platform` | read history, ensure channels, un-react, send select menus, upsert status |
| `NewCommandSource(c, token)` | `contracts.CommandSource` | hold the bot's gateway websocket, surface `INTERACTION_CREATE` as inbound commands |
| `NewResponder(c, appID)` | `contracts.CommandResponder` | defer / respond / edit / autocomplete / ack-component |
| `NewRegistrar(c)` | `contracts.CommandRegistrar` | publish the slash-command tree |
| `NewProber(c)` | `contracts.Prober` | cheap `/users/@me` round-trip for health latency |
| `NewStatusReporter(c, ch)` | `contracts.StatusReporter` | maintain a self-updating status message |
| `NewChannelAdmin(c)` | core's `ChannelAdmin` | create-under / forum-post / archive / send / kind |

The [host](../herrscher-host/README.md) wires these into `serve.Deps`, wrapping the
Gateway in `contracts.Degrade(...)` so the core can always call the richest method.

---

## The command source

`CommandSource.Run(ctx)` opens and maintains the bot's gateway websocket, handles
the heartbeat (feeding a `contracts.Liveness` sink via `SetLiveness` for connection
health), and translates each `INTERACTION_CREATE` into a
`contracts.InboundCommand` — slash command, clicked component, or autocomplete
request — pushed onto the `Commands()` channel for the core's dispatch loop. It runs
until the context is cancelled or the connection drops.

---

## The slash-command schema

`commands.go` declares the full command tree the registrar publishes. It mirrors the
handlers implemented in the [core](../herrscher-core/README.md):

| Command | Subcommands / options |
|---------|-----------------------|
| `/set` | `home <channel>`, `workspace <path>`, `source <path>` |
| `/session` | `create [name] [cmd] [shared] [backend: stream\|oneshot] [project] [clone]`, `close [name] [force]`, `list`, `who [name]`, `allow add/remove/list` |
| `/workspace` | `list`, `remotes [forge]` |
| `/service` | `restart`, `update [no-pull]` |
| `/allow` | `add <user>`, `remove <user>`, `list` |

The `cmd`, `project` and `clone` options are marked `autocomplete` so the core can
suggest values (model presets, local projects, remote repos) live as the user types.

---

## Select-menu choice routing

When the core posts a select menu, the `custom_id` carries the **session name**, not
the channel — so a click can be routed back to the right per-session control socket.
`choice.go` provides the codec:

```go
func ChoiceCustomID(session string) string         // "dctlchoice:" + session
func ParseChoiceCustomID(id string) (string, bool)  // extract the session name
```

---

## Layout

| File | Contents |
|------|----------|
| `gateway.go` | `Gateway` adapter + `Manifest` |
| `source.go` | `CommandSource`: websocket lifecycle, heartbeat, `INTERACTION_CREATE` → `InboundCommand` |
| `commands.go` | declarative slash-command schema + `Register` |
| `adapters.go` | `ChannelAdmin`, `Platform`, `Responder`, `Registrar`, `Prober`, `StatusReporter` |
| `choice.go` | select-menu `custom_id` codec |

---

## Build & test

```bash
go build ./...
go vet ./...
go test ./...   # 7 tests
```

Go 1.23. Depends on `dctl`, `herrscher-contracts`, and `coder/websocket` (the first
two wired locally via `replace` directives). Pure library — no binary. The
[host](../herrscher-host/README.md) is the only thing that constructs these adapters.
