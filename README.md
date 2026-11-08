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
| `owner` | `DISCORD_USER_ID` | | the user the bot always obeys, whatever the allowlists say |
| `allowed_users` | `DISCORD_ALLOWED_USERS` | | user ids allowed to make the bot act (`*` for anyone) |
| `allowed_roles` | `DISCORD_ALLOWED_ROLES` | | role ids allowed to, guild messages only |
| `allowed_channels` | `DISCORD_ALLOWED_CHANNELS` | every channel | the only channels it answers in |
| `ignored_channels` | `DISCORD_IGNORED_CHANNELS` | | channels it never answers in, whatever the rest allows |
| `allow_all_users` | `DISCORD_ALLOW_ALL_USERS` | off | let anyone who can reach the bot make it act |
| `allow_bots` | `DISCORD_ALLOW_BOTS` | `none` | whether other bots count as people: `none`, `mentions`, `all` |
| `require_mention` | `DISCORD_REQUIRE_MENTION` | on | need an @mention in a channel |
| `thread_require_mention` | `DISCORD_THREAD_REQUIRE_MENTION` | off | need one inside a thread the gateway opened too |
| `free_response_channels` | `DISCORD_FREE_RESPONSE_CHANNELS` | | channels it answers in unprompted (`*` for all) |
| `sessions_per_user` | `DISCORD_SESSIONS_PER_USER` | on | each participant of a channel gets their own session |
| `thread_sessions_per_user` | `DISCORD_THREAD_SESSIONS_PER_USER` | off | same inside a thread |
| `agent` | `DISCORD_AGENT` | | the persona every session runs as, which is what gates its tools |
| `verbosity` | `DISCORD_VERBOSITY` | `silent` | how much of a turn the channel sees, see below |
| `context_messages` | `DISCORD_CONTEXT_MESSAGES` | `30` | how many messages of channel history a turn carries |
| `playbook` | `DISCORD_PLAYBOOK` | `pr-job` | the playbook a session opens with |
| `tidy_pings` | `DISCORD_TIDY_PINGS` | off | delete the ping once its answer is posted, see below |
| `message_deletes` | `DISCORD_MESSAGE_DELETES` | off | contribute `discord message delete`, see below |
| `heads_file` | `DISCORD_HEADS_FILE` | built-in heads | JSON file overriding the emoji heads, see below |
| | `DCTL_STATE_DIR` | `~/.config/dctl` | where the router and allow stores live |

Ports implemented: `Gateway`, `EventSink`, `RoutedEventSink`,
`SessionControlReceiver`, `ChannelReader`, `MenuRouter`, `ChannelAdmin`,
`Prober`, `CommandSource`, `MessageEditor`, `NoticeSink`.

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
| `silent` (default) | the ⏳, then only the answer |
| `quiet` | also shows a live list of tool names, never a path, command or search pattern from your machine |
| `actions` | also shows each tool's detail |
| `full` | also shows the assistant's thinking |

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

## Who it answers, and when

The gateway identifies with `GUILDS`, `GUILD_MESSAGES` and `DIRECT_MESSAGES`, all
three non-privileged. Every message it sees passes three gates, in this order,
and a message that clears all three becomes a turn.

**1. The mode of the room.** `/mode` puts one channel in one of four:

| Mode | What the room does |
|------|--------------------|
| `normal` (default) | one session per room, pings answered under the ordinary rule |
| `support` | every ping opens its own private thread, so the channel stays a queue |
| `ambient` | the bot answers without being addressed at all |
| `off` | the bot answers nothing here, even when @mentioned |

A thread the gateway opened inherits the mode of the channel it hangs off. The
mode survives a restart, in `discord-router.json` with everything else the room
is remembered by.

`ambient` is refused unless `agent` / `DISCORD_AGENT` names a persona. A bot that
answers everyone in a room without being called is bounded by exactly one thing:
the persona its sessions run as, which is what carries the tool allowlist, the
MCP servers and the approval hook. Without one, ambient is a bot with no guard.

**2. Was it meant for the bot.** An @mention or a reply to one of its own
messages always counts. A DM always counts. A thread the gateway opened counts
without a mention, unless `thread_require_mention` is on. A channel listed in
`free_response_channels` counts without one too. Everything else needs a mention,
unless `require_mention` is off.

**3. May this person make it act.** In order: `owner`, then `allowed_users`, then
`allowed_roles` (guild messages only, since a DM carries no role), then
`allow_all_users`. If none of those are configured but `allowed_channels` is, the
channel is the policy and everyone who can write there is allowed. If nothing at
all is configured the bot refuses and warns once in the daemon log, rather than
obeying whoever found it first. `ignored_channels` overrides everything, and
`allow_bots` (`none` by default, or `mentions`, or `all`) decides whether other
bots count as people, because two bots that answer each other is a loop.

All four id lists take a comma-separated list of ids and accept `*` for "anyone".

Two things these lists do not cover. `allowed_channels` and `ignored_channels`
match a thread through the parent the gateway itself recorded, so a thread
somebody else created under an ignored channel is not seen as belonging to it:
Discord does not carry a parent on the message event. List such a thread by its
own id if it matters. And this is a different question from `/allow`, which says
who may run the bot's slash commands. These lists say who may make the bot
*work*; the allow store says who may *drive* it.

## One session per person, or one per room

A channel gives each participant their own session by default, so two people
pinging the same room are two conversations that never see each other's work. A
thread does the opposite: it is one piece of work, and everyone in it shares one
session. `sessions_per_user` and `thread_sessions_per_user` flip either rule.

In a shared session every turn is prefixed with `[author]` and the opening turn
says so, so the agent answers whoever just spoke instead of assuming the room is
one person. A DM is neither: it is a room with one person in it.

Deleting a room closes every session it held, not just one.

## What the first ping decides

Everyone's messages are context: the last `context_messages` messages of the
channel are read over REST and carried into the turn, whoever wrote them. The
first ping in an unknown channel is acked with ⏳ right away, since it may be
answered by a question rather than by a turn. That ping decides two things:

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

`/set`, `/session`, `/service`, `/allow`, `/stop`, `/mode` and `/verbosity` are registered
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

## Languages

The gateway writes its own texts in the language of the conversation they concern:
`Notify` renders a notice in the language of the session bound to its channel when
that session has one, and otherwise in the language the host passes with it. Texts
that no notice carries (the repo menus, the prompts of a ping) use the operator's language, read from `HERRSCHER_LANG` when the gateway starts: `fr` for
French, `en` for English. When `HERRSCHER_LANG` is absent the texts are in English.
A value the gateway does not support, such as `de`, is logged once as a warning and
the texts stay in English. Nothing is repaired silently.

The ephemeral replies of the slash commands follow the language of the person who
typed the command, which Discord sends with each interaction as its locale. A
French client gets French replies, and a locale the gateway does not support falls
back to the operator's language. This holds in a bound session too: an ephemeral
reply is seen by its invoker alone, so the invoker's locale decides, not the
session's language.

In a channel bound to a session, the texts the gateway writes there, such as the
notices about repos and threads, the message that could not be delivered and the
progress lines, use the session's language when the session has one, and the
operator's language otherwise. The menu replies (the repo and choice menus) are
public and shared by everyone in the channel, so they stay in the operator's
language even there.

The slash commands are registered in English, with a French description for each
command, option and choice, so the Discord client shows the descriptions in French
to French users. The names stay identical in both languages, because they are fixed
syntax.

The gateway reads the variable itself because contracts v0.22.0 has no way for the
host to pass the operator's default language to a plugin, only the language of each
session (`SessionInfo.Language`) and of each notice. The variable is therefore set in
the environment of the daemon that loads the gateway.

The texts live in two catalogs, `locales/en.json` and `locales/fr.json`, in the
go-i18n format. A test checks that the French catalog has every English key with
the same placeholders, and that the code uses no key the catalog lacks. A key
missing from French falls back to its English text, with one warning in the log.

Two things stay as they are whatever the language. The detail of an unexpected
error is the engine's own text, shown verbatim in a code block with mentions
neutralised, and the output of commands an agent reads stays English.

## Emoji heads

Every line the gateway puts in front of a message carries a head: the ⏳ on a
ping, the ❌ of a failure, the ✅ of a finished turn, the icon of each tool in the
progress view, and the icon of each notice code. Each head has a stable key. The
built-in heads live in `heads/en.json` and `heads/fr.json`, in the go-i18n format
(`id` is the key, `other` the head), and are loaded at startup. A head missing
from a language falls back to its English head. A head missing from English is
empty, and the line is posted with no leading space.

`DISCORD_HEADS_FILE` points to a JSON object from key to head. It applies to every
language:

```json
{"ack": "👀", "tool.read": "", "session.closed": "🛑"}
```

An empty value turns a head off: no reaction is added for it, and its line has no
prefix. A key the gateway does not know, or a file that cannot be read or parsed,
stops the gateway at startup. The error carries a `setting.invalid` notice that
names the setting, for the host to show the operator.

| Key | Shown on |
|---|---|
| `ack` | the reaction on a ping the agent has taken up, removed when the turn ends |
| `running` | the header of the live progress message |
| `fail` | the reaction on a failure, a failure line, a notice that could not be rendered |
| `done` | a finished turn, the progress summary |
| `stop` | the confirmation of `/stop` |
| `thinking` | the assistant's text at the full verbosity level |
| `tool.read` | Read |
| `tool.edit` | Edit, Write, MultiEdit, NotebookEdit |
| `tool.search` | Grep, Glob |
| `tool.agent` | Task, Agent |
| `tool.web` | WebFetch, WebSearch |
| `tool.write` | TodoWrite |
| `tool.other` | any other tool |
| `session.not_found`, `session.exists`, `agent.unknown` | the refused session or agent |
| `session.created` | a session that was created |
| `session.closed` | a session that was closed |
| `compaction.done` | a compaction that finished |
| `compact.unavailable` | a compaction that cannot run |
| `internal.unexpected` | an unexpected internal error |
| `setting.invalid`, `setting.missing` | a setting that is wrong or absent |
| `node.merged` | a node folded into an umbrella |
| `address.plaintext_refused` | a plaintext address that was refused |

## When a notice cannot be rendered

When a notice cannot be rendered, `Notify` posts a fallback text that names the
failure, then returns the render error. That error satisfies
`errors.Is(err, contracts.ErrNoticeFallbackPosted)`, which says the fallback is already in the channel.
The host must not call `Notify` again for that notice, nor post `Degrade` behind it, or
the operator receives the message twice. If the fallback itself cannot be posted,
`Notify` returns the error of the post, which does not satisfy `contracts.ErrNoticeFallbackPosted`:
the gateway posted nothing for that notice, so the host decides whether to retry.

## Reconnection

The gateway websocket reconnects with exponential backoff until the daemon context
is cancelled.

A non-recoverable close is treated as **fatal**. 4004 (bad token) and 4010 to 4014
(bad shard, API version, intents) stop the loop and log once, rather than
respamming a doomed IDENTIFY. Fix the token or the intents, then restart.

## Health and limits

The plugin sets `Plugin.Probe`, so `herrscher health` can check the bot without
a live gateway. The probe reads the token the way the factory does: the `token`
setting from `ProbeAccess.Config`, then `DISCORD_BOT_TOKEN` from
`ProbeAccess.Getenv` when the setting is empty.

It is read only. The one call it makes is `GET /api/v10/users/@me` with
`Authorization: Bot <token>` and the same User-Agent as dctl; it never posts, reacts or opens the websocket, and
`--deep` changes nothing because the cheap check is already a real round trip.

| Finding | State | Detail | Remedy |
|---|---|---|---|
| no token | off | `health.env_missing` (`DISCORD_BOT_TOKEN`) | `health.remedy_env_restart` (`DISCORD_BOT_TOKEN`) |
| 200 | ok, latency measured | `health.signed_in_as` (the bot username) | none |
| 401 | down | `health.credentials_refused` | none |
| 429 | degraded | `health.rate_limited` | none |
| 200 unreadable or nameless | degraded | `health.unreachable` (`invalid response` or `no bot name in response`) | none |
| 5xx, other status, network error, deadline, cancel | degraded | `health.unreachable` with a short reason (`http 503`, `timeout`, `canceled`, `connection refused`, `dns lookup failed`, `network error`, `invalid request`) | none |

The reason never carries the token or the request URL. The probe stops when the
host's context ends, and reports instead of panicking. A gateway has no vendor
quota, so the plugin sets no `Limits`.

## Further reading

- [Herrscher docs](https://github.com/Herrscherd/herrscher-docs), page
  `plugins/gateway`
- [contracts](https://github.com/Herrscherd/herrscher-contracts), for the port
  signatures
