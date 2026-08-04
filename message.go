package discord

import "github.com/Herrscherd/dctl"

// messageCreate is the subset of a Discord MESSAGE_CREATE dispatch the gateway
// reads. dctl.Message covers the REST shape but carries neither `mentions` nor
// `referenced_message`, and both are load-bearing here: they are how the trigger
// filter tells "the owner is talking to the bot" from ordinary channel chatter.
// Declaring the shape here keeps dctl untouched.
type messageCreate struct {
	ID          string            `json:"id"`
	ChannelID   string            `json:"channel_id"`
	GuildID     string            `json:"guild_id"`
	Content     string            `json:"content"`
	Author      dctl.Author       `json:"author"`
	Mentions    []dctl.Author     `json:"mentions"`
	Attachments []dctl.Attachment `json:"attachments"`
	// Referenced is the message this one replies to, present only when Discord
	// resolves it. A reply to a message the bot wrote is a trigger; nil is not.
	Referenced *referencedMessage `json:"referenced_message"`
}

// referencedMessage is the replied-to message, narrowed to its author — the only
// field the trigger filter needs.
type referencedMessage struct {
	Author dctl.Author `json:"author"`
}
