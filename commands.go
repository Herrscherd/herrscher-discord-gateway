package discord

import (
	"context"
	"errors"
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
// Help and parameter texts are catalog keys, so a host resolves them in its own
// language. It satisfies contracts.CommandSource.
func (g *Gateway) Commands() []contracts.Cmd {
	cmds := []contracts.Cmd{
		contracts.New("channel", "read").
			Help("help.channel_read").
			Param("id", "param.channel_id", true).
			ValueParam("limit", "param.limit", false).
			ValueParam("after", "param.after", false).
			Do(g.readChannel),
		contracts.New("channel", "post").
			Help("help.channel_post").
			Param("id", "param.channel_id", true).
			Param("text", "param.text", true).
			Do(g.postChannel),
		contracts.New("message", "reply").
			Help("help.message_reply").
			Param("id", "param.channel_id", true).
			Param("to", "param.reply_to", true).
			Param("text", "param.text", true).
			Do(g.replyMessage),
		contracts.New("message", "react").
			Help("help.message_react").
			Param("id", "param.channel_id", true).
			Param("msg", "param.message_id", true).
			Param("emoji", "param.emoji_add", true).
			Do(g.reactMessage),
		contracts.New("message", "unreact").
			Help("help.message_unreact").
			Param("id", "param.channel_id", true).
			Param("msg", "param.message_id", true).
			Param("emoji", "param.emoji_remove", true).
			Do(g.unreactMessage),
		contracts.New("message", "edit").
			Help("help.message_edit").
			Param("id", "param.channel_id", true).
			Param("msg", "param.message_id", true).
			Param("text", "param.new_body", true).
			Do(g.editMessage),
	}
	if g.deletes {
		cmds = append(cmds, contracts.New("message", "delete").
			Help("help.message_delete").
			Param("id", "param.channel_id", true).
			Param("msg", "param.message_id", true).
			Do(g.deleteMessage))
	}
	return cmds
}

func (g *Gateway) readChannel(ctx context.Context, input contracts.Input) (contracts.Result, error) {
	limit, _ := input.Lookup("limit")
	after, _ := input.Lookup("after")
	msgs, err := g.c.ReadMessages(ctx, input.Args["id"], capLimit(limit, readCap), after)
	if err != nil {
		return contracts.Result{}, fmt.Errorf("discord channel read: %w", err)
	}
	return contracts.DataResult(renderMessages(g.msgs, msgs)), nil
}

func (g *Gateway) postChannel(ctx context.Context, in contracts.Input) (contracts.Result, error) {
	sent, err := g.c.Send(ctx, in.Args["id"], in.Args["text"])
	if err != nil {
		return contracts.Result{}, fmt.Errorf("discord channel post: %w", err)
	}
	return contracts.DataResult("posted " + string(msgID(sent))), nil
}

func (g *Gateway) replyMessage(ctx context.Context, in contracts.Input) (contracts.Result, error) {
	sent, err := g.c.Reply(ctx, in.Args["id"], in.Args["to"], in.Args["text"])
	if err != nil {
		return contracts.Result{}, fmt.Errorf("discord message reply: %w", err)
	}
	return contracts.DataResult("replied " + string(msgID(sent))), nil
}

func (g *Gateway) reactMessage(ctx context.Context, in contracts.Input) (contracts.Result, error) {
	if err := g.c.React(ctx, in.Args["id"], in.Args["msg"], in.Args["emoji"]); err != nil {
		return contracts.Result{}, fmt.Errorf("discord message react: %w", err)
	}
	return contracts.DataResult("reacted"), nil
}

func (g *Gateway) unreactMessage(ctx context.Context, in contracts.Input) (contracts.Result, error) {
	if err := g.c.Unreact(ctx, in.Args["id"], in.Args["msg"], in.Args["emoji"]); err != nil {
		return contracts.Result{}, fmt.Errorf("discord message unreact: %w", err)
	}
	return contracts.DataResult("unreacted"), nil
}

func (g *Gateway) editMessage(ctx context.Context, in contracts.Input) (contracts.Result, error) {
	if err := g.Edit(ctx, in.Args["id"], in.Args["msg"], in.Args["text"]); err != nil {
		return contracts.Result{}, err
	}
	return contracts.DataResult("edited"), nil
}

func (g *Gateway) deleteMessage(ctx context.Context, input contracts.Input) (contracts.Result, error) {
	if err := g.ownMessage(ctx, input.Args["id"], input.Args["msg"]); err != nil {
		return contracts.Result{}, err
	}
	if err := g.Delete(ctx, input.Args["id"], input.Args["msg"]); err != nil {
		return contracts.Result{}, err
	}
	return contracts.DataResult("deleted"), nil
}

var (
	errOwnIDUnknown  = errors.New("this bot's own id is unknown, so authorship cannot be checked")
	errMessageGone   = errors.New("no such message")
	errNotOwnMessage = errors.New("only this bot's own messages can be deleted")
)

func (g *Gateway) ownMessage(ctx context.Context, channelID, messageID string) error {
	if g.selfID == "" {
		return fmt.Errorf("discord message delete: %w", errOwnIDUnknown)
	}
	fetched, err := g.c.GetMessage(ctx, channelID, messageID)
	if err != nil {
		return fmt.Errorf("discord message delete: read message %s: %w", messageID, err)
	}
	if fetched == nil {
		return fmt.Errorf("discord message delete: message %s is not in channel %s: %w", messageID, channelID, errMessageGone)
	}
	if fetched.Author.ID != g.selfID {
		return fmt.Errorf("discord message delete: message %s was written by %s, not by this bot: %w", messageID, authorOf(*fetched), errNotOwnMessage)
	}
	return nil
}

func capLimit(v string, ceiling int) int {
	number, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || number < 1 || number > ceiling {
		return ceiling
	}
	return number
}

func renderMessages(texts messages, msgs []dctl.Message) string {
	if len(msgs) == 0 {
		return texts.say("command.no_messages", nil)
	}
	var builder strings.Builder
	for i := range msgs {
		msg := msgs[i]
		fmt.Fprintf(&builder, "%s %s: %s  [%s]\n",
			authorOf(msg), msg.Timestamp, bodyOf(texts, msg), msg.ID)
	}
	return builder.String()
}

func bodyOf(texts messages, msg dctl.Message) string {
	body := flattenLines(strings.TrimSpace(msg.Content))
	if body != "" {
		return body
	}
	if n := len(msg.Attachments); n > 0 {
		return texts.say("command.attachments", map[string]any{countArg: n, nameArg: neutralise(msg.Attachments[0].Filename)})
	}
	if len(msg.Embeds) > 0 {
		return "(embed)"
	}
	return ""
}

func flattenLines(text string) string {
	r := strings.NewReplacer("\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(text)
}

func authorOf(msg dctl.Message) string {
	if msg.Author.Username != "" {
		return msg.Author.Username
	}
	return msg.Author.ID
}
