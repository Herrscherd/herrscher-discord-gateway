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
reaction on the triggering message, and a final reply that opens with the owner's
@mention and is chunked at Discord's 2000-rune limit. The mention is not
decoration — a turn runs for minutes and its answer is a plain post, which
notifies nobody. A turn that ends with nothing to say still posts a line, because
a ⏳ that merely vanishes reads like a bot that died.

Above the default level it also draws one live-updating progress message per turn
(15 lines, trimmed further if they would exceed the message limit, one edit per
1.5 s) collapsed to a ✅ summary at the end; a mid-turn backend reset discards the
partial render and keeps going, and an abandoned turn clears the ACK silently.

`DISCORD_VERBOSITY` sets how much of that reaches a channel by default:

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

`/verbosity level:…` retunes **one conversation**, takes effect on the next turn
and survives a restart (`discord-router.json`). The level belongs to the room,
not to the job: the same operator wants a live tool trace in his own thread and
nothing but the answer in a channel his team reads, and the env var can only say
one of those, daemon-wide, until the next restart. A job that moves into a
private thread takes its channel's level with it.

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

Asking for a thread works in a channel that already has a session too: the job
**forks** into a private thread of its own, on the same repo — that question was
answered when the channel was bound, and asking it again would be friction. The
channel keeps its own session for the pings that stay there.

A message that does not @mention the bot arrives from the websocket with an empty
body: `MESSAGE_CONTENT` is a privileged intent this gateway deliberately does not
ask for. Those messages are read back over REST, which the intent does not gate,
so a bare message in a thread costs one extra call and no privilege. The bot role
needs `Create Private Threads` and `Send Messages in Threads`.

## Replying to something is pointing at it

Ping the bot **as a reply** to another message and that message is the request:
"@Herrscher regarde ça" under someone's bug report says nothing on its own. The
replied-to message is re-read by id — it arrives blanked like any other, and it
is whatever you scrolled back to rather than something recent — and quoted at the
head of the turn, right before your instruction.

Everything hanging off it comes too: its uploads, and the images inside its
embeds, which is where a bot posts its screenshots. They are handed to the host
as attachments, after your own uploads — the host caps how many files it
downloads per message, and what you attached yourself comes first. An embed image
Discord did not mirror onto its own CDN is named in the text but not handed over:
the host would refuse it, and the refusal would spend a download slot.

The same reason keeps embeds in the channel context: a message with no text is
not an empty message. A bot that reports through embeds used to be invisible to
the agent, which in a channel full of bot reports is most of what there is to
read.

Either way the answer creates a session that **adopts that conversation** and is
remembered in `discord-router.json` (mode 0600, under `DCTL_STATE_DIR`).
Rendering is per conversation: each one gets its own progress message, ⏳ ack and
reply.

## The slash surface

`/set`, `/session`, `/service`, `/allow`, `/stop` and `/verbosity` are registered **globally** — on the
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

`/stop` cancels the turn running in the conversation it is typed in — it takes no
session name, because the room already says which session that is. It reaches
`SessionControl.Interrupt`, which stops the backend turn and keeps the
conversation: the next message picks up where it left off. Without it the only
way out of a turn gone wrong is `/session close`, which also throws away the
worktree and everything in it.

## Reconnection

The gateway websocket reconnects with exponential backoff until the daemon context
is cancelled. A non-recoverable close is treated as **fatal**: 4004 (bad token) and
4010–4014 (bad shard / API version / intents) stop the loop and log once rather than
respamming a doomed IDENTIFY. Fix the token or the intents, then restart.

## Further reading

- [Herrscher docs](https://github.com/Herrscherd/herrscher-docs) — `plugins/gateway`
- [contracts](https://github.com/Herrscherd/herrscher-contracts) — port signatures
