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
	// Its body arrives blanked for the same reason this message's would be, and
	// it is whatever the operator scrolled back to rather than something recent,
	// so it is a name to re-read rather than content — see router.reference.
	Referenced *dctl.Message `json:"referenced_message"`
}
