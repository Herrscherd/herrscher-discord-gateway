package discord

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

const channelCategory = 4

// ChannelAdmin adapts the dctl client to contracts.ChannelAdmin: session channel
// creation/archival and posting.
type ChannelAdmin struct{ bots *voiceBots }

// NewChannelAdmin returns a ChannelAdmin that works through the bots of the gateway.
func NewChannelAdmin(bots *voiceBots) *ChannelAdmin { return &ChannelAdmin{bots: bots} }

// Kind reports what the channel id names: "category" for a category, "forum" for a
// forum, and an empty string for any other channel.
func (a *ChannelAdmin) Kind(ctx context.Context, id string) (string, error) {
	target := a.bots.at(id)
	kind, err := target.client.Channels().Type(ctx, target.channel)
	if err != nil {
		return "", fmt.Errorf("discord channel type: %w", err)
	}
	switch kind {
	case channelCategory:
		return "category", nil
	case dctl.ChannelForum:
		return "forum", nil
	default:
		return "", nil
	}
}

// CreateUnder creates a channel named name under parentID and returns its id. It
// returns an empty id when Discord answers without a channel.
func (a *ChannelAdmin) CreateUnder(ctx context.Context, parentID, name string) (string, error) {
	target := a.bots.at(parentID)
	created, err := target.client.Channels().CreateUnder(ctx, target.channel, name)
	if err != nil {
		return "", fmt.Errorf("discord create channel: %w", err)
	}
	if created == nil {
		return "", nil
	}
	return created.ID, nil
}

// ForumPost opens a post named name with content in the forum forumID and returns
// the id of the post's thread. It returns an empty id when Discord answers without
// a thread.
func (a *ChannelAdmin) ForumPost(ctx context.Context, forumID, name, content string) (string, error) {
	target := a.bots.at(forumID)
	post, err := target.client.Threads().ForumPost(ctx, target.channel, name, content)
	if err != nil {
		return "", fmt.Errorf("discord forum post: %w", err)
	}
	if post == nil {
		return "", nil
	}
	return post.ID, nil
}

// Archive archives the channel id. It returns an error that wraps the Discord
// failure.
func (a *ChannelAdmin) Archive(ctx context.Context, id string) error {
	target := a.bots.at(id)
	if err := target.client.Channels().Archive(ctx, target.channel); err != nil {
		return fmt.Errorf("discord archive channel: %w", err)
	}
	return nil
}

// Send posts content as a message in channelID. It returns an error that wraps
// the Discord failure.
func (a *ChannelAdmin) Send(ctx context.Context, channelID, content string) error {
	target := a.bots.at(channelID)
	var failed error
	for _, part := range chunkText(content, target.limit()) {
		if err := target.post(ctx, part); err != nil {
			failed = errors.Join(failed, fmt.Errorf("discord send: %w", err))
		}
	}
	return failed
}

// ChannelRef renders a Discord channel id as channel-mention markup so operator
// output links to the channel.
func (a *ChannelAdmin) ChannelRef(id string) string { return "<#" + id + ">" }

// Platform adapts the dctl client to the neutral channel ports
// contracts.ChannelReader and contracts.MenuRouter (the consumer's read/
// channel-bootstrap/reaction/status/routed-menu surface).
type Platform struct {
	bots  *voiceBots
	sinks *sinks
	binds *bindStore
}

type rawMsg struct {
	id  string
	bot bool
	msg contracts.Message
}

// NewPlatform returns a Platform that works through the bots of the gateway.
func NewPlatform(bots *voiceBots) *Platform {
	return &Platform{bots: bots}
}

// Enabled reports whether the house dctl client is enabled.
func (p *Platform) Enabled() bool { return p.bots.house.Enabled() }

// DefaultChannel returns the default channel id of the house dctl client.
func (p *Platform) DefaultChannel() string { return p.bots.house.DefaultChannel() }

// EnsureChannel returns the channel named name, created under parentID when parentID
// is set and at the top level otherwise. It returns an error that wraps the Discord
// failure.
func (p *Platform) EnsureChannel(ctx context.Context, parentID, name string) (contracts.Channel, error) {
	ensure := func() (*dctl.Channel, error) {
		if parentID != "" {
			target := p.bots.at(parentID)
			return target.client.Channels().EnsureUnder(ctx, target.channel, name)
		}
		return p.bots.house.Channels().Ensure(ctx, "", name)
	}
	channel, err := ensure()
	if err != nil {
		return contracts.Channel{}, fmt.Errorf("discord ensure channel: %w", err)
	}
	if channel == nil {
		return contracts.Channel{}, nil
	}
	return contracts.Channel{ID: channel.ID, Name: channel.Name}, nil
}

func rawMessages(msgs []dctl.Message) []rawMsg {
	out := make([]rawMsg, 0, len(msgs))
	for i := range msgs {
		msg := msgs[i]
		atts := make([]contracts.Attachment, 0, len(msg.Attachments))
		for _, attachment := range msg.Attachments {
			atts = append(atts, contracts.Attachment{
				Filename:    attachment.Filename,
				URL:         attachment.URL,
				ContentType: attachment.ContentType,
				Size:        attachment.Size,
			})
		}
		out = append(out, rawMsg{
			id:  msg.ID,
			bot: msg.Author.Bot,
			msg: contracts.Message{
				ID:          msg.ID,
				ChannelID:   msg.ChannelID,
				Content:     msg.Content,
				AuthorID:    msg.Author.ID,
				AuthorName:  msg.Author.Username,
				AuthorBot:   msg.Author.Bot,
				Attachments: atts,
			},
		})
	}
	return out
}

// Read returns recent channel messages and records the id of the last non-bot
// message so the next turn's ACK reaction lands on it. A channel the router
// drives by push returns nothing: that channel already has an inbound path, and
// two would deliver every message twice.
func (p *Platform) Read(ctx context.Context, channelID string, limit int, after string) ([]contracts.Message, error) {
	if p.binds != nil && p.binds.Session(channelID) != "" {
		return nil, nil
	}
	target := p.bots.at(channelID)
	msgs, err := target.client.Messages().Read(ctx, target.channel, limit, after)
	if err != nil {
		return nil, fmt.Errorf("discord read: %w", err)
	}
	raws := rawMessages(msgs)
	for idx := range raws {
		if raws[idx].msg.ChannelID == target.channel {
			raws[idx].msg.ChannelID = channelID
		}
	}
	out := make([]contracts.Message, 0, len(raws))
	for i := range raws {
		out = append(out, raws[i].msg)
	}
	p.noteHumanReads(ctx, raws)
	return out, nil
}

func (p *Platform) noteHumanReads(ctx context.Context, raws []rawMsg) {
	if p.sinks == nil {
		return
	}
	for i := range raws {
		raw := raws[i]
		if !raw.bot {
			p.sinks.at(raw.msg.ChannelID).noteUser(ctx, raw.msg.ChannelID, raw.id, raw.msg.AuthorID)
		}
	}
}

// Unreact removes the emoji from the message messageID in channelID. It returns an
// error that wraps the Discord failure.
func (p *Platform) Unreact(ctx context.Context, channelID, messageID, emoji string) error {
	target := p.bots.at(channelID)
	if err := target.client.Reactions().Remove(ctx, target.channel, messageID, emoji); err != nil {
		return fmt.Errorf("discord unreact: %w", err)
	}
	return nil
}

// UpsertStatusMessage edits the message messageID in channelID to content. When
// messageID is empty, or Discord no longer has that message, it posts content as a
// new message. It returns the id of the message it edited or posted, and an error
// that wraps the Discord failure.
func (p *Platform) UpsertStatusMessage(ctx context.Context, channelID, messageID, content string) (string, error) {
	target := p.bots.at(channelID)
	text, err := target.frame(content)
	if err != nil {
		return "", fmt.Errorf("discord status message: %w", err)
	}
	statusID, err := target.client.Interactions().UpsertStatusMessage(ctx, target.channel, messageID, text)
	if err != nil {
		return statusID, fmt.Errorf("discord status message: %w", err)
	}
	return statusID, nil
}

// RouteMenu sends prompt in reply to replyTo with a select menu of opts whose picks
// are routed to the session route. It returns the id of the menu message.
func (p *Platform) RouteMenu(ctx context.Context, channelID, replyTo, prompt, route string, opts []contracts.Choice) (contracts.MessageID, error) {
	out := make([]dctl.SelectOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, dctl.SelectOption{Label: o.Label, Value: o.Value})
	}
	target := p.bots.menuAt(channelID)
	text, err := target.frame(prompt)
	if err != nil {
		return "", fmt.Errorf("discord menu: %w", err)
	}
	sent, err := p.bots.house.Components().SendSelectMenu(ctx, target.channel, replyTo, text, ChoiceCustomID(route), out)
	if err != nil {
		return "", fmt.Errorf("discord menu: %w", err)
	}
	if sent == nil {
		return "", nil
	}
	return contracts.MessageID(sent.ID), nil
}

type renderAdapter struct{ p *Platform }

var _ renderClient = renderAdapter{}

func (r renderAdapter) UpsertStatusMessage(ctx context.Context, ch, id, content string) (string, error) {
	return r.p.UpsertStatusMessage(ctx, ch, id, content)
}

func (r renderAdapter) Unreact(ctx context.Context, ch, id, emoji string) error {
	return r.p.Unreact(ctx, ch, id, emoji)
}

func (r renderAdapter) TextLimit(ch string) int {
	return r.p.bots.textLimit(ch)
}

func (r renderAdapter) Post(ctx context.Context, ch, content string) error {
	if err := r.p.bots.at(ch).post(ctx, content); err != nil {
		return fmt.Errorf("discord post: %w", err)
	}
	return nil
}

func (r renderAdapter) React(ctx context.Context, ch, messageID, emoji string) error {
	target := r.p.bots.at(ch)
	if err := target.client.Reactions().Add(ctx, target.channel, messageID, emoji); err != nil {
		return fmt.Errorf("discord react: %w", err)
	}
	return nil
}

func (r renderAdapter) Delete(ctx context.Context, ch, messageID string) error {
	target := r.p.bots.at(ch)
	if err := target.client.Messages().Delete(ctx, target.channel, messageID); err != nil {
		return fmt.Errorf("discord delete: %w", err)
	}
	return nil
}

// Prober adapts a cheap REST round-trip (/users/@me) to contracts.Prober.
type Prober struct{ c *dctl.Client }

// NewProber returns a Prober that measures round-trips through the dctl client c.
func NewProber(c *dctl.Client) *Prober { return &Prober{c: c} }

// Probe measures one REST round-trip to Discord and returns its duration in
// milliseconds. On failure it returns the duration of the failed attempt and an error
// that wraps the Discord failure.
func (p *Prober) Probe(ctx context.Context) (int64, error) {
	start := time.Now()
	_, err := p.c.Interactions().AppID(ctx)
	if err != nil {
		return time.Since(start).Milliseconds(), fmt.Errorf("discord probe: %w", err)
	}
	return time.Since(start).Milliseconds(), nil
}

var (
	_ contracts.ChannelReader = (*Platform)(nil)
	_ contracts.MenuRouter    = (*Platform)(nil)
	_ contracts.ChannelAdmin  = (*ChannelAdmin)(nil)
)
