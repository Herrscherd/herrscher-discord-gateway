package discord

import "github.com/Herrscherd/dctl"

type messageCreate struct {
	ID          string            `json:"id"`
	ChannelID   string            `json:"channel_id"`
	GuildID     string            `json:"guild_id"`
	Content     string            `json:"content"`
	Author      dctl.Author       `json:"author"`
	Mentions    []dctl.Author     `json:"mentions"`
	Attachments []dctl.Attachment `json:"attachments"`
	Referenced  *dctl.Message     `json:"referenced_message"`
	Member      *guildMember      `json:"member"`
}

type guildMember struct {
	Roles []string `json:"roles"`
}

func (m messageCreate) direct() bool { return m.GuildID == "" }

func (m messageCreate) roleIDs() []string {
	if m.Member == nil {
		return nil
	}
	return m.Member.Roles
}
