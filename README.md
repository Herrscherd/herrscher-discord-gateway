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
| **Role** | Receives Discord slash interactions, posts replies, and renders turn progress in-channel |
| **Category** | Gateway (inbound edge) |
| **Ports implemented** | `Gateway`, `EventSink`, `SessionControlReceiver`, `ChannelReader`, `MenuRouter`, `ChannelAdmin`, `Prober` |
| **Config & env** | `token` / `DISCORD_BOT_TOKEN` (**required**), `channel` / `DISCORD_CHANNEL_ID` (default channel id), `DCTL_STATE_DIR` (default: `~/.config/dctl`) |
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
Mono-channel by design: one bot, one default channel, one in-flight turn.

## The slash surface

`/set`, `/session`, `/service` and `/allow` are declared and parsed here. Commands the
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
