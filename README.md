# herrscher-discord-gateway

**The Discord edge.** It adapts the [`dctl`](https://github.com/Herrscherd/dctl)
Discord client to the Herrscher gateway ports and self-registers from `init()`,
so a host enables Discord with a blank import and a rebuild.

It is a pure plugin: no `main`, no composition root. It is also a *smart*
gateway. It implements `EventSink` and renders the live turn stream itself, so
the core never learns anything Discord-specific.

Status: live.

## Install

```bash
herrscher plugin add github.com/Herrscherd/herrscher-discord-gateway
```

## Configuration

Each setting has a config key and an environment variable. The two are
interchangeable.

| Key | Environment | Default | What it is |
|---|---|---|---|
| `token` | `DISCORD_BOT_TOKEN` | **required** | the bot token |
| `owner` | `DISCORD_USER_ID` | **required** | the user the bot obeys |
| `verbosity` | `DISCORD_VERBOSITY` | `silent` | how much of a turn the channel sees, see below |
| `context_messages` | `DISCORD_CONTEXT_MESSAGES` | `30` | how many messages of channel history a turn carries |
| `playbook` | `DISCORD_PLAYBOOK` | `pr-job` | the playbook a session opens with |
| `tidy_pings` | `DISCORD_TIDY_PINGS` | off | delete the ping once its answer is posted, see below |
| `message_deletes` | `DISCORD_MESSAGE_DELETES` | off | contribute `discord message delete`, see below |
| | `DCTL_STATE_DIR` | `~/.config/dctl` | where the router and allow stores live |

Ports implemented: `Gateway`, `EventSink`, `RoutedEventSink`,
`SessionControlReceiver`, `ChannelReader`, `MenuRouter`, `ChannelAdmin`,
`Prober`, `CommandSource`, `MessageEditor`.

## Rendering happens here, not in the core

The gateway receives the raw turn-event stream and draws Discord itself: a ⏳ ack
reaction on the triggering message, and a final reply that opens with the owner's
@mention and is chunked at Discord's 2000-rune limit.

The mention is not decoration. A turn runs for minutes and its answer is a plain
post, which notifies nobody. A turn that ends with nothing to say still posts a
line, because a ⏳ that merely vanishes reads like a bot that died.

The ⏳ is a state, never a destination. Every path that marks a ping as taken
ends either in a turn, which clears it, or in a ❌ on the same message plus one
line saying why, in the channel the ping was written in. A repo menu that could
not be posted, a session the daemon refused to create: those used to reach only
the daemon's stderr, leaving an hourglass that nothing would ever come back to.

Above the default level the gateway also draws one live-updating progress message
per turn (15 lines, trimmed further if they would exceed the message limit, one
edit per 1.5 s), collapsed to a ✅ summary at the end. A mid-turn backend reset
discards the partial render and keeps going, and an abandoned turn clears the ack
silently.

### Verbosity

`DISCORD_VERBOSITY` sets how much of that reaches a channel by default.

| Level | What the channel sees |
|---|---|
| `silent` (default) | the ⏳, then the answer, nothing else |
| `quiet` | adds a live list of tool names, with no path, command or search pattern from your machine |
| `actions` | adds each tool's detail |
| `full` | adds the assistant's thinking |

`silent` is the default because this bot is meant to be pinged in rooms other
people read, where a running commentary of tool calls is noise. Raise it when you
want to watch a turn work. Repeated lines collapse to `×N`, and the ✅ summary
(tool names, count, duration, cost) is the same at every level that has one.

`/verbosity level:…` retunes **one conversation**, takes effect on the next turn,
and survives a restart (`discord-router.json`). The level belongs to the room,
not to the job: the same operator wants a live tool trace in his own thread and
nothing but the answer in a channel his team reads, and the env var can only say
one of those, daemon-wide, until the next restart. A job that moves into a
private thread takes its channel's level with it.

## Owner-bound, not channel-bound

The gateway identifies with `GUILDS`, `GUILD_MESSAGES` and `DIRECT_MESSAGES`, all
three non-privileged, and acts on a message only when the configured owner
@mentions the bot or replies to it.

Everyone else's messages are never triggers, but the last `context_messages`
messages of the channel are read over REST and carried into the turn, so the
agent sees the whole conversation.

The first ping in an unknown channel is acked with ⏳ right away, since it may be
answered by a question rather than by a turn. That ping decides two things.

**Which repo.** Name it in the message ("le bug d'auth de *herrscher*", by bare
name or as `owner/repo`) and the work starts immediately. Name none or two, and a
select menu asks, once, and only the first time ever. After that the last repo you
picked carries over to the next room, named out loud when it does, so a wrong
carry-over is visible immediately (`/session close` starts over). A repo that has
since disappeared brings the menu back rather than binding blind.

**Where.** Ask for a thread ("dans un fil", "ouvre un thread", "en privé") and the
job gets a **private** thread: `invitable: false`, with you added as its only
human member, and a single line in the channel linking it, because the answer is
going to land somewhere the channel cannot show. Everything after that (questions,
progress, the answer) happens inside it, and a plain message there needs no
@mention. Server moderators holding `Manage Threads` can still see it, and nobody
else can. If the thread cannot be created, the job falls back to the channel and
says so out loud.

Asking for a thread works in a channel that already has a session too. The job
**forks** into a private thread of its own, on the same repo, since that question
was answered when the channel was bound and asking it again would be friction.
The channel keeps its own session for the pings that stay there.

A message that does not @mention the bot arrives from the websocket with an empty
body: `MESSAGE_CONTENT` is a privileged intent this gateway deliberately does not
ask for. Those messages are read back over REST, which the intent does not gate,
so a bare message in a thread costs one extra call and no privilege.

The bot role needs `Create Private Threads` and `Send Messages in Threads`.

## Replying to something is pointing at it

Ping the bot **as a reply** to another message and that message is the request.
"@Herrscher regarde ça" under someone's bug report says nothing on its own. The
replied-to message is re-read by id, and quoted at the head of the turn right
before your instruction. It arrives blanked like any other, and it is whatever you
scrolled back to rather than something recent.

Everything hanging off it comes too: its uploads, and the images inside its
embeds, which is where a bot posts its screenshots. They are handed to the host as
attachments, after your own uploads, because the host caps how many files it
downloads per message and what you attached yourself comes first. An embed image
Discord did not mirror onto its own CDN is named in the text but not handed over:
the host would refuse it, and the refusal would spend a download slot.

The same reason keeps embeds in the channel context. A message with no text is not
an empty message, and a bot that reports through embeds used to be invisible to
the agent, which in a channel full of bot reports is most of what there is to
read.

Either way the answer creates a session that **adopts that conversation** and is
remembered in `discord-router.json` (mode 0600, under `DCTL_STATE_DIR`). Rendering
is per conversation: each one gets its own progress message, ⏳ ack and reply.

## Deleting the room ends the job

Delete a channel or a thread and the session driving it is closed.

Nothing else would. A session's bridge is supervised and restarted forever, and
only an explicit close stops it, so a conversation that disappears out from under
one leaves an agent running against a room that no longer exists, burning tokens
on a turn nobody will read and failing every reply it posts. That is why the
gateway takes the `GUILDS` intent: it is what carries `CHANNEL_DELETE` and
`THREAD_DELETE`.

Deleting a channel takes its threads with it, and Discord announces only the
channel. So the gateway remembers which channel each thread it opened hangs off,
and closes those sessions too. Without that, the rule would miss the case it
exists for, since a job asked to run "dans un fil" lives in exactly such a thread.

The close prefers the non-destructive form. If it is refused because the agent
left uncommitted changes in its worktree, the forcing one is taken and said out
loud in the daemon log. There is nobody left to ask to commit, since the
conversation that would carry the question is the one that was deleted. Only
uncommitted changes go: every commit the agent made is on branch
`session/<name>`, which close keeps either way.

## Tidying up your own pings

With `DISCORD_TIDY_PINGS=true`, the message that opened a turn is deleted once its
answer is posted, so a channel reads as a log of work rather than of requests for
it. It is off by default because deleting a message is not undoable, and it is
deliberately narrow.

- **Only for a turn that produced an answer.** A turn that died keeps its ping. It
  is the only remaining record of what was wanted.
- **Only for a message the configured owner wrote.** A turn can be acked on
  somebody else's message, since the channel reader records the last non-bot one
  whoever sent it, and deleting that is not tidying, it is moderating.
- **Only for a ping written in the very conversation the answer landed in.** This
  is not caution but correctness: a support channel starts its thread **on** the
  ping, and Discord deletes a thread along with the message it hangs off. The same
  rule leaves alone the ping in a public channel for a job that forked into a
  private thread.

So in a support-mode channel, and for a forked job, nothing is ever deleted. It
applies to an ordinary channel holding one conversation, where the ping and the
answer live side by side. The bot role needs `Manage Messages`. Without it the
delete is refused and the ⏳ is cleared the ordinary way instead.

## What the plugin adds to the daemon

Compiling this gateway in adds two things beyond the Discord edge itself.

The verbs below join the daemon's command registry, namespaced by the host under
this plugin's kind: the plugin declares `channel read`, an operator types
`discord channel read`.

And the playbook `skills/discord-conversations/SKILL.md` is installed into
`~/.claude/skills` the first time the binary runs, so an agent learns when to
reach for those verbs on a machine that actually has them. An existing file of the
same name is never overwritten.

A read renders one line per message: author, timestamp, body, and the message id
last so a follow-up can page with `--after`. The body is flattened onto that line
on purpose. The line is the unit the agent reads, and a message allowed to carry a
newline could otherwise forge a second one in the same shape.

### Letting an agent delete a message

The contributed verbs are `discord channel read`, `channel post`,
`message reply`, `react`, `unreact` and `edit`. Those are visible, small and
undoable.

`discord message delete` is none of those things, and it is reachable from the
same agent context that `discord channel read` fills with text other people wrote.

So it is not contributed at all unless `DISCORD_MESSAGE_DELETES=true`. Off, which
is the default, the verb is absent from the command list rather than present and
failing: a command that was never registered cannot be talked into running by a
message asking for it. Turning it on is one deliberate decision an operator takes
once, in config, off Discord.

On, the verb still refuses any message this bot did not write. Discord bounds
`edit` to the bot's own messages itself, but hands `delete` to anyone holding
`Manage Messages`, so the gateway checks the author before it deletes, and refuses
rather than guesses when it cannot read the message back.

## The slash surface

`/set`, `/session`, `/service`, `/allow`, `/stop` and `/verbosity` are registered
**globally**, on the application rather than on a server, so the bot carries its
commands into every server it is invited to and no server has to be named in
config. Discord takes up to an hour to propagate a change to a global command.

Commands the core owns become neutral argv through `SessionControl.Dispatch`.
`/allow` and `/session allow` mutate a plugin-local store the core never sees
(`discord-allow.json`, mode 0600, under `DCTL_STATE_DIR`).

Three gates stack:

1. Discord's `default_member_permissions = Manage Server`;
2. the allow store (empty means everyone who can see the command, so the first
   operator can bootstrap; populated means listed users only);
3. the daemon's own role table, described below.

Treat the store as the real policy for who reaches the gateway. The Discord
default is overridable by a guild admin.

`/stop` cancels the turn running in the conversation it is typed in. It takes no
session name, because the room already says which session that is. It reaches
`SessionControl.Interrupt`, which stops the backend turn and keeps the
conversation, so the next message picks up where it left off. Without it the only
way out of a turn gone wrong is `/session close`, which also throws away the
worktree and everything in it.

## Who is asking

Every slash command carries its caller to the daemon as `discord:<user id>`. The
gateway names the human, because Discord is the only side that knows, and the
daemon decides what that name may run.

The two answers stay separate on purpose. The allow store says who may talk to
this gateway at all. The role says what the daemon will do for them once they can.

Until somebody holds a role, the daemon decides nothing about humans, and this
changes nothing about how the bot behaves. The first grant ends that for everyone
at once: from then on every account without a role is an `observer`, so name the
others before they find out.

```bash
herrscher role grant discord:1234 operator --label leo
```

The id is Discord's own, the same one `/allow add` takes. A refusal spells the
principal out, so you can copy it back rather than look it up.

## Reconnection

The gateway websocket reconnects with exponential backoff until the daemon context
is cancelled.

A non-recoverable close is treated as **fatal**. 4004 (bad token) and 4010 to 4014
(bad shard, API version, intents) stop the loop and log once, rather than
respamming a doomed IDENTIFY. Fix the token or the intents, then restart.

## Further reading

- [Herrscher docs](https://github.com/Herrscherd/herrscher-docs), page
  `plugins/gateway`
- [contracts](https://github.com/Herrscherd/herrscher-contracts), for the port
  signatures
