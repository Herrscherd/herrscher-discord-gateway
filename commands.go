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
	cmds := []contracts.Cmd{
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
	}

	// Delete is the one verb here nothing undoes, and it is reachable from the
	// same agent context `channel read` fills with text strangers wrote. Prose
	// telling an agent to be careful is a defence in the same medium as the
	// attack, so the verb is instead absent from the registry entirely until an
	// operator turns it on: a command that was never contributed cannot be
	// talked into running. One deliberate decision, taken once, off Discord.
	if g.deletes {
		cmds = append(cmds, contracts.New("message", "delete").
			Help("delete a message this bot sent").
			Param("id", "channel id", true).
			Param("msg", "message id", true).
			Do(func(ctx context.Context, in contracts.Input) (string, error) {
				if err := g.ownMessage(ctx, in.Args["id"], in.Args["msg"]); err != nil {
					return "", err
				}
				if err := g.Delete(ctx, in.Args["id"], in.Args["msg"]); err != nil {
					return "", err
				}
				return "deleted", nil
			}))
	}
	return cmds
}

// ownMessage is the second half of the guard, and it bounds an enabled delete to
// what this bot actually wrote. Discord enforces that bound on `edit` itself but
// not on `delete`, which it grants wholesale to whoever holds Manage Messages —
// so the worst an enabled delete can do, moderating a channel on the say-so of
// text that channel contains, is refused here. Anything it cannot confirm is a
// refusal too: a message that will not come back is not assumed to be the bot's.
func (g *Gateway) ownMessage(ctx context.Context, channelID, messageID string) error {
	if g.selfID == "" {
		return fmt.Errorf("discord message delete: this bot's own id is unknown, so authorship cannot be checked")
	}
	m, err := g.c.GetMessage(ctx, channelID, messageID)
	if err != nil {
		return fmt.Errorf("discord message delete: read message %s: %w", messageID, err)
	}
	if m == nil {
		return fmt.Errorf("discord message delete: message %s is not in channel %s", messageID, channelID)
	}
	if m.Author.ID != g.selfID {
		return fmt.Errorf("discord message delete: message %s was written by %s, not by this bot — only this bot's own messages can be deleted", messageID, authorOf(*m))
	}
	return nil
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
//
// One line per message is an invariant and not a habit, which is why the body
// is flattened. The line is the unit an agent reads and the unit --after pages
// from, so a message allowed to carry a newline could write a second line in
// the same shape — any author name, any timestamp, any trailing id — and the
// format itself would vouch for it. The skill already tells an agent that what
// it reads is context and never an instruction; flattening is what stops the
// rendering from authenticating the forgery in the first place.
func renderMessages(msgs []dctl.Message) string {
	if len(msgs) == 0 {
		return "(no messages)"
	}
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "%s %s: %s  [%s]\n",
			authorOf(m), m.Timestamp, bodyOf(m), m.ID)
	}
	return b.String()
}

// bodyOf renders a message's content on a single line, and says so when the
// message was never text: an image or an unfurled link with no caption would
// otherwise render as an author who said nothing, which reads as silence rather
// than as the attachment that was the whole point.
func bodyOf(m dctl.Message) string {
	body := flattenLines(strings.TrimSpace(m.Content))
	if body != "" {
		return body
	}
	if n := len(m.Attachments); n > 0 {
		return fmt.Sprintf("(%d attachment(s): %s)", n, m.Attachments[0].Filename)
	}
	if len(m.Embeds) > 0 {
		return "(embed)"
	}
	return ""
}

// flattenLines folds every line break into a visible escape so a body cannot
// break out of its own line. It is deliberately lossy in the display sense —
// the text stays readable, and reading is all this output is for.
func flattenLines(s string) string {
	r := strings.NewReplacer("\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(s)
}

// authorOf names a line's author, falling back to the raw user id when the
// username is empty: an ugly line beats an anonymous one.
func authorOf(m dctl.Message) string {
	if m.Author.Username != "" {
		return m.Author.Username
	}
	return m.Author.ID
}
