# herrscher-discord-gateway

**The Discord channel edge.** Adapts the [`dctl`](https://github.com/Herrscherd/dctl)
Discord REST client to the Herrscher gateway ports and self-registers from `init()`,
so a host enables Discord with a blank import and a rebuild. It is a pure plugin —
no `main`, no composition root — and it is a *smart* gateway: it implements
`EventSink` and renders the live turn stream itself, so the core never learns
anything Discord-specific.

## Role · Category · Ports · Config · Status · Repo

| Aspect | Value |
|--------|-------|
| **Role** | Receives Discord mentions and slash interactions, posts replies, and renders turn progress in-channel |
| **Category** | Gateway (inbound edge) |
| **Ports implemented** | `Gateway`, `EventSink`, `RoutedEventSink`, `SessionControlReceiver`, `ChannelReader`, `MenuRouter`, `ChannelAdmin`, `Prober` |
| **Config & env** | `token` / `DISCORD_BOT_TOKEN` (**required**), `owner` / `DISCORD_USER_ID` (**required**, the user the bot obeys), `verbosity` / `DISCORD_VERBOSITY` (`quiet` default / `actions` / `full` — see below), `context_messages` / `DISCORD_CONTEXT_MESSAGES` (default 30), `playbook` / `DISCORD_PLAYBOOK` (default `pr-job`), `DCTL_STATE_DIR` (default: `~/.config/dctl`) |
| **Status** | live |
| **Repo** | [herrscher-discord-gateway](https://github.com/Herrscherd/herrscher-discord-gateway) |

## Install

```bash
herrscher plugin add github.com/Herrscherd/herrscher-discord-gateway
```

## Rendering happens here, not in the core

The Gateway receives the raw turn-event stream and draws Discord itself: one
live-updating progress message per turn (capped at 15 lines, one edit per 1.5 s),
a ⏳ ACK reaction on the triggering message, and a final reply chunked at Discord's
2000-rune limit and collapsed to a ✅ summary. A mid-turn backend reset discards the
partial render and keeps going; an abandoned turn clears the ACK silently.

`DISCORD_VERBOSITY` sets how much of that reaches the channel. `quiet` (default)
posts tool names only, so no absolute path, shell command or search pattern from
your machine is ever shown — the level to leave alone when the bot is pinged in
a channel other people read. `actions` adds each tool's detail; `full` also adds
the assistant's thinking. Repeated lines collapse to `×N`, and the ✅ summary
(tool names, count, duration, cost) is the same at every level.

## Owner-bound, not channel-bound

The gateway identifies with `GUILD_MESSAGES` and `DIRECT_MESSAGES` — both
non-privileged — and acts on a message only when the configured owner @mentions
the bot or replies to it. Everyone else's messages are never triggers, but the
last `context_messages` messages of the channel are read over REST and carried
into the turn, so the agent sees the whole conversation. The first ping in an
unknown channel is acked with ⏳ and asks which repo to work on with a select
menu — the ack lands right away, since that ping is answered by a question
rather than by a turn; the answer creates
a session that **adopts that channel** and is remembered in `discord-router.json`
(mode 0600, under `DCTL_STATE_DIR`). Rendering is per conversation: each channel
gets its own progress message, ⏳ ack and reply.

## The slash surface

`/set`, `/session`, `/service` and `/allow` are registered **globally** — on the
application, not on a server — so the bot carries its commands into every server
it is invited to and no server has to be named in config. Discord takes up to an
hour to propagate a change to a global command. Commands the
core owns become neutral argv through `SessionControl.Dispatch`; `/allow` and
`/session allow` mutate a plugin-local store the core never sees
(`discord-allow.json`, mode 0600, under `DCTL_STATE_DIR`). Two gates stack: Discord's
`default_member_permissions = Manage Server`, plus that store — empty means everyone
who can see the command (so the first operator can bootstrap), populated means
listed users only. Treat the store as the real policy; the Discord default is
overridable by a guild admin.

## Reconnection

The gateway websocket reconnects with exponential backoff until the daemon context
is cancelled. A non-recoverable close is treated as **fatal**: 4004 (bad token) and
4010–4014 (bad shard / API version / intents) stop the loop and log once rather than
respamming a doomed IDENTIFY. Fix the token or the intents, then restart.

## Further reading

- [Herrscher docs](https://github.com/Herrscherd/herrscher-docs) — `plugins/gateway`
- [contracts](https://github.com/Herrscherd/herrscher-contracts) — port signatures
