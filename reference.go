package discord

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

func attachmentHostList() []string {
	return []string{
		"cdn.discordapp.com",
		"media.discordapp.net",
		"images-ext-1.discordapp.net",
		"images-ext-2.discordapp.net",
	}
}

func isAttachmentHost(host string) bool {
	return slices.Contains(attachmentHostList(), host)
}

func (r *router) reference(ctx context.Context, msg messageCreate) *dctl.Message {
	if msg.Referenced == nil {
		return nil
	}
	ref := msg.Referenced
	if carries(*ref) {
		return ref
	}
	if ref.ID == "" {
		return nil
	}
	channel := ref.ChannelID
	if channel == "" {
		channel = msg.ChannelID
	}
	got, err := r.c.GetMessage(ctx, channel, ref.ID)
	if err != nil {
		r.log.Warn("read replied-to message failed", "err", err)
		return nil
	}
	if got == nil || !carries(*got) {
		return nil
	}
	return got
}

func carries(m dctl.Message) bool {
	return strings.TrimSpace(m.Content) != "" || len(m.Attachments) > 0 || len(m.Embeds) > 0
}

func quote(ref *dctl.Message) string {
	if ref == nil {
		return ""
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Message being replied to, from %s:\n", ref.Author.Username)
	if body := strings.TrimSpace(ref.Content); body != "" {
		for _, line := range strings.Split(body, "\n") {
			fmt.Fprintf(&builder, "> %s\n", line)
		}
	}
	for _, line := range describe(*ref) {
		fmt.Fprintf(&builder, "> %s\n", line)
	}
	return builder.String()
}

const describeMax = 600

func describe(msg dctl.Message) []string {
	var out []string
	add := func(text string) {
		if text = clip(flatten(text), describeMax); text != "" {
			out = append(out, text)
		}
	}
	for _, a := range msg.Attachments {
		add("[fichier] " + a.Filename)
	}
	for _, embed := range msg.Embeds {
		if head := embedHead(embed); head != "" {
			add("[embed] " + head)
		}
		for _, field := range embed.Fields {
			if field.Name == "" && field.Value == "" {
				continue
			}
			add(fmt.Sprintf("[embed] %s: %s", field.Name, field.Value))
		}
		if u := mediaURL(embed.Image); u != "" {
			add("[image] " + label(u))
		} else if u := mediaURL(embed.Thumbnail); u != "" {
			add("[image] " + label(u))
		}
	}
	return out
}

func embedHead(embed dctl.Embed) string {
	var parts []string
	if embed.Title != "" {
		parts = append(parts, embed.Title)
	}
	if embed.Description != "" {
		parts = append(parts, embed.Description)
	}
	head := strings.Join(parts, ", ")
	if embed.URL != "" {
		if head == "" {
			return embed.URL
		}
		return head + " (" + embed.URL + ")"
	}
	return head
}

func mediaURL(media *dctl.EmbedMedia) string {
	if media == nil {
		return ""
	}
	if media.ProxyURL != "" {
		return media.ProxyURL
	}
	return media.URL
}

func label(rawURL string) string {
	if name, ok := cdnName(rawURL); ok {
		return name
	}
	return rawURL
}

func cdnName(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !isAttachmentHost(strings.ToLower(parsed.Hostname())) {
		return "", false
	}
	name := path.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		return "", false
	}
	return name, true
}

func attachmentsOf(m messageCreate, ref *dctl.Message) []contracts.Attachment {
	out := uploads(m.Attachments)
	if ref == nil {
		return out
	}
	out = append(out, uploads(ref.Attachments)...)
	return append(out, embedImages(*ref)...)
}

func uploads(attachments []dctl.Attachment) []contracts.Attachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]contracts.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, contracts.Attachment{
			Filename:    attachment.Filename,
			URL:         attachment.URL,
			ContentType: attachment.ContentType,
			Size:        attachment.Size,
		})
	}
	return out
}

func embedImages(msg dctl.Message) []contracts.Attachment {
	var out []contracts.Attachment
	seen := make(map[string]bool)
	for _, embed := range msg.Embeds {
		imageURL := mediaURL(embed.Image)
		if imageURL == "" {
			imageURL = mediaURL(embed.Thumbnail)
		}
		name, ok := cdnName(imageURL)
		if !ok || seen[imageURL] {
			continue
		}
		seen[imageURL] = true
		out = append(out, contracts.Attachment{Filename: name, URL: imageURL})
	}
	return out
}
