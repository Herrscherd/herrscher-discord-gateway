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
| **Config & env** | `token` / `DISCORD_BOT_TOKEN` (**required**), `owner` / `DISCORD_USER_ID` (**required**, the user the bot obeys), `verbosity` / `DISCORD_VERBOSITY` (`silent` default / `quiet` / `actions` / `full` — see below), `context_messages` / `DISCORD_CONTEXT_MESSAGES` (default 30), `playbook` / `DISCORD_PLAYBOOK` (default `pr-job`), `DCTL_STATE_DIR` (default: `~/.config/dctl`) |
| **Status** | live |
| **Repo** | [herrscher-discord-gateway](https://github.com/Herrscherd/herrscher-discord-gateway) |

## Install

```bash
herrscher plugin add github.com/Herrscherd/herrscher-discord-gateway
```

## Rendering happens here, not in the core

The Gateway receives the raw turn-event stream and draws Discord itself: a ⏳ ACK
reaction on the triggering message, and a final reply chunked at Discord's
2000-rune limit. Above the default level it also draws one live-updating progress
message per turn (capped at 15 lines, one edit per 1.5 s) collapsed to a ✅
summary at the end; a mid-turn backend reset discards the partial render and
keeps going, and an abandoned turn clears the ACK silently.

`DISCORD_VERBOSITY` sets how much of that reaches the channel:

| Level | What the channel sees |
|-------|-----------------------|
| `silent` (default) | the ⏳, then the answer — nothing else |
| `quiet` | adds a live list of tool names, with no path, command or search pattern from your machine |
| `actions` | adds each tool's detail |
| `full` | adds the assistant's thinking |

`silent` is the default because this bot is meant to be pinged in rooms other
people read, where a running commentary of tool calls is noise; raise it when you
want to watch a turn work. Repeated lines collapse to `×N`, and the ✅ summary
(tool names, count, duration, cost) is the same at every level that has one.

## Owner-bound, not channel-bound

The gateway identifies with `GUILD_MESSAGES` and `DIRECT_MESSAGES` — both
non-privileged — and acts on a message only when the configured owner @mentions
the bot or replies to it. Everyone else's messages are never triggers, but the
last `context_messages` messages of the channel are read over REST and carried
into the turn, so the agent sees the whole conversation. The first ping in an
unknown channel is acked with ⏳ right away, since it may be answered by a
question rather than by a turn. That ping decides two things:

- **Which repo.** Name it in the message ("le bug d'auth de *herrscher*", by bare
  name or as `owner/repo`) and the work starts immediately. Name none or two, and
  a select menu asks — once.
- **Where.** Ask for a thread ("dans un fil", "ouvre un thread", "en privé") and
  the job gets a **private** thread: created off the channel so it leaves no trace
  there, `invitable: false`, with you added as its only human member. Everything
  after that — questions, progress, the answer — happens inside it, and a plain
  message there needs no @mention. Server moderators holding `Manage Threads` can
  still see it; nobody else can. If the thread cannot be created, the job falls
  back to the channel and says so out loud.

Either way the answer creates a session that **adopts that conversation** and is
remembered in `discord-router.json` (mode 0600, under `DCTL_STATE_DIR`).
Rendering is per conversation: each one gets its own progress message, ⏳ ack and
reply.

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
