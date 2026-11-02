package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

var _ contracts.CommandSource = (*Gateway)(nil)

const readCap = 100

// Commands are the verbs this gateway contributes to the daemon's registry. They
// are declared unprefixed, the host namespaces them under this plugin's kind,
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
		return fmt.Errorf("discord message delete: message %s was written by %s, not by this bot, only this bot's own messages can be deleted", messageID, authorOf(*m))
	}
	return nil
}

func capLimit(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 || n > readCap {
		return readCap
	}
	return n
}

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

func flattenLines(s string) string {
	r := strings.NewReplacer("\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(s)
}

func authorOf(m dctl.Message) string {
	if m.Author.Username != "" {
		return m.Author.Username
	}
	return m.Author.ID
}
