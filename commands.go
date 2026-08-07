package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts"
)

var _ contracts.CommandSource = (*Gateway)(nil)

// readCap bounds one channel read. It is the value the daemon's own poller
// already uses, and it is a cap rather than a default because the caller is an
// agent: asking for five thousand messages would drown its own context and take
// Discord's rate limit in the process.
const readCap = 100

// Commands are the verbs this gateway contributes to the daemon's registry. They
// are declared unprefixed — the host namespaces them under this plugin's kind,
// so what an operator types is `discord channel read`. Spelling "discord" here
// would produce `discord discord channel read`.
//
// It satisfies contracts.CommandSource.
func (g *Gateway) Commands() []contracts.Cmd {
	return []contracts.Cmd{
		contracts.New("channel", "read").
			Help("read a conversation: the recent messages of a channel, oldest first").
			Param("id", "channel id", true).
			ValueParam("limit", fmt.Sprintf("how many messages, capped at %d", readCap), false).
			ValueParam("after", "message id to read forward from, for paging", false).
			Do(func(ctx context.Context, in contracts.Input) (string, error) {
				limit, _ := in.Lookup("limit")
				after, _ := in.Lookup("after")
				// The read goes to the raw client, never through
				// contracts.ChannelReader.Read: that one answers nil, nil for a
				// channel already bound to a session, so reading the session's own
				// channel would come back empty with no error at all.
				msgs, err := g.c.ReadMessages(ctx, in.Args["id"], capLimit(limit), after)
				if err != nil {
					return "", fmt.Errorf("discord channel read: %w", err)
				}
				return renderMessages(msgs), nil
			}),

		contracts.New("channel", "post").
			Help("post a message to a channel").
			Param("id", "channel id", true).
			Param("text", "message body", true).
			Do(func(ctx context.Context, in contracts.Input) (string, error) {
				m, err := g.c.Send(ctx, in.Args["id"], in.Args["text"])
				if err != nil {
					return "", fmt.Errorf("discord channel post: %w", err)
				}
				return "posted " + string(msgID(m)), nil
			}),

		contracts.New("message", "reply").
			Help("reply to a specific message").
			Param("id", "channel id", true).
			Param("to", "message id being replied to", true).
			Param("text", "message body", true).
			Do(func(ctx context.Context, in contracts.Input) (string, error) {
				m, err := g.c.Reply(ctx, in.Args["id"], in.Args["to"], in.Args["text"])
				if err != nil {
					return "", fmt.Errorf("discord message reply: %w", err)
				}
				return "replied " + string(msgID(m)), nil
			}),

		contracts.New("message", "react").
			Help("add a reaction to a message").
			Param("id", "channel id", true).
			Param("msg", "message id", true).
			Param("emoji", "emoji to add", true).
			Do(func(ctx context.Context, in contracts.Input) (string, error) {
				if err := g.c.React(ctx, in.Args["id"], in.Args["msg"], in.Args["emoji"]); err != nil {
					return "", fmt.Errorf("discord message react: %w", err)
				}
				return "reacted", nil
			}),

		contracts.New("message", "unreact").
			Help("remove a reaction this bot added to a message").
			Param("id", "channel id", true).
			Param("msg", "message id", true).
			Param("emoji", "emoji to remove", true).
			Do(func(ctx context.Context, in contracts.Input) (string, error) {
				if err := g.c.Unreact(ctx, in.Args["id"], in.Args["msg"], in.Args["emoji"]); err != nil {
					return "", fmt.Errorf("discord message unreact: %w", err)
				}
				return "unreacted", nil
			}),

		contracts.New("message", "edit").
			Help("rewrite a message this bot sent").
			Param("id", "channel id", true).
			Param("msg", "message id", true).
			Param("text", "new body", true).
			Do(func(ctx context.Context, in contracts.Input) (string, error) {
				if err := g.Edit(ctx, in.Args["id"], in.Args["msg"], in.Args["text"]); err != nil {
					return "", err
				}
				return "edited", nil
			}),

		contracts.New("message", "delete").
			Help("delete a message").
			Param("id", "channel id", true).
			Param("msg", "message id", true).
			Do(func(ctx context.Context, in contracts.Input) (string, error) {
				if err := g.Delete(ctx, in.Args["id"], in.Args["msg"]); err != nil {
					return "", err
				}
				return "deleted", nil
			}),
	}
}

// capLimit turns the caller's --limit into a read size. An unparseable or absent
// value falls back to the cap rather than to zero: a typo must not silently read
// nothing and report an empty channel.
func capLimit(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 || n > readCap {
		return readCap
	}
	return n
}

// renderMessages lays a channel out for an agent to read: one line per message,
// author first, the id last so a follow-up can page with --after. Text and not
// JSON on purpose — this lands in an agent's context, not in a parser.
//
// Bot messages are kept. The poller drops them because it is manufacturing
// turns; a read the operator asked for must show the channel as it is, the
// bot's own answers included.
func renderMessages(msgs []dctl.Message) string {
	if len(msgs) == 0 {
		return "(no messages)"
	}
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "%s %s: %s  [%s]\n",
			authorOf(m), m.Timestamp, strings.TrimSpace(m.Content), m.ID)
	}
	return b.String()
}

// authorOf names a line's author, falling back to the raw user id when the
// username is empty: an ugly line beats an anonymous one.
func authorOf(m dctl.Message) string {
	if m.Author.Username != "" {
		return m.Author.Username
	}
	return m.Author.ID
}
