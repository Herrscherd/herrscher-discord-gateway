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

func (r *router) reference(ctx context.Context, m messageCreate) *dctl.Message {
	if m.Referenced == nil {
		return nil
	}
	ref := m.Referenced
	if carries(*ref) {
		return ref
	}
	if ref.ID == "" {
		return nil
	}
	channel := ref.ChannelID
	if channel == "" {
		channel = m.ChannelID
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
	var b strings.Builder
	fmt.Fprintf(&b, "Message auquel il répond, %s :\n", ref.Author.Username)
	if body := strings.TrimSpace(ref.Content); body != "" {
		for _, line := range strings.Split(body, "\n") {
			fmt.Fprintf(&b, "> %s\n", line)
		}
	}
	for _, line := range describe(*ref) {
		fmt.Fprintf(&b, "> %s\n", line)
	}
	return b.String()
}

const describeMax = 600

func describe(m dctl.Message) []string {
	var out []string
	add := func(s string) {
		if s = clip(flatten(s), describeMax); s != "" {
			out = append(out, s)
		}
	}
	for _, a := range m.Attachments {
		add("[fichier] " + a.Filename)
	}
	for _, e := range m.Embeds {
		if head := embedHead(e); head != "" {
			add("[embed] " + head)
		}
		for _, f := range e.Fields {
			if f.Name == "" && f.Value == "" {
				continue
			}
			add(fmt.Sprintf("[embed] %s : %s", f.Name, f.Value))
		}
		if u := mediaURL(e.Image); u != "" {
			add("[image] " + label(u))
		} else if u := mediaURL(e.Thumbnail); u != "" {
			add("[image] " + label(u))
		}
	}
	return out
}

func embedHead(e dctl.Embed) string {
	var parts []string
	if e.Title != "" {
		parts = append(parts, e.Title)
	}
	if e.Description != "" {
		parts = append(parts, e.Description)
	}
	head := strings.Join(parts, ", ")
	if e.URL != "" {
		if head == "" {
			return e.URL
		}
		return head + " (" + e.URL + ")"
	}
	return head
}

func mediaURL(m *dctl.EmbedMedia) string {
	if m == nil {
		return ""
	}
	if m.ProxyURL != "" {
		return m.ProxyURL
	}
	return m.URL
}

func label(u string) string {
	if name, ok := cdnName(u); ok {
		return name
	}
	return u
}

func cdnName(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !isAttachmentHost(strings.ToLower(u.Hostname())) {
		return "", false
	}
	name := path.Base(u.Path)
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

func uploads(as []dctl.Attachment) []contracts.Attachment {
	if len(as) == 0 {
		return nil
	}
	out := make([]contracts.Attachment, 0, len(as))
	for _, a := range as {
		out = append(out, contracts.Attachment{
			Filename:    a.Filename,
			URL:         a.URL,
			ContentType: a.ContentType,
			Size:        a.Size,
		})
	}
	return out
}

func embedImages(m dctl.Message) []contracts.Attachment {
	var out []contracts.Attachment
	seen := make(map[string]bool)
	for _, e := range m.Embeds {
		u := mediaURL(e.Image)
		if u == "" {
			u = mediaURL(e.Thumbnail)
		}
		name, ok := cdnName(u)
		if !ok || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, contracts.Attachment{Filename: name, URL: u})
	}
	return out
}
