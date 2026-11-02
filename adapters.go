package discord

import (
	"context"
	"fmt"
	"time"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

const channelCategory = 4

// ChannelAdmin adapts the dctl client to contracts.ChannelAdmin: session channel
// creation/archival and posting.
type ChannelAdmin struct{ c *dctl.Client }

// NewChannelAdmin returns a ChannelAdmin that works through the dctl client c.
func NewChannelAdmin(c *dctl.Client) *ChannelAdmin { return &ChannelAdmin{c: c} }

// Kind reports what the channel id names: "category" for a category, "forum" for a
// forum, and an empty string for any other channel.
func (a *ChannelAdmin) Kind(ctx context.Context, id string) (string, error) {
	kind, err := a.c.Channels().Type(ctx, id)
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
	channel, err := a.c.Channels().CreateUnder(ctx, parentID, name)
	if err != nil {
		return "", fmt.Errorf("discord create channel: %w", err)
	}
	if channel == nil {
		return "", nil
	}
	return channel.ID, nil
}

// ForumPost opens a post named name with content in the forum forumID and returns
// the id of the post's thread. It returns an empty id when Discord answers without
// a thread.
func (a *ChannelAdmin) ForumPost(ctx context.Context, forumID, name, content string) (string, error) {
	channel, err := a.c.Threads().ForumPost(ctx, forumID, name, content)
	if err != nil {
		return "", fmt.Errorf("discord forum post: %w", err)
	}
	if channel == nil {
		return "", nil
	}
	return channel.ID, nil
}

// Archive archives the channel id. It returns an error that wraps the Discord
// failure.
func (a *ChannelAdmin) Archive(ctx context.Context, id string) error {
	if err := a.c.Channels().Archive(ctx, id); err != nil {
		return fmt.Errorf("discord archive channel: %w", err)
	}
	return nil
}

// Send posts content as a message in channelID. It returns an error that wraps
// the Discord failure.
func (a *ChannelAdmin) Send(ctx context.Context, channelID, content string) error {
	if _, err := a.c.Messages().Send(ctx, channelID, content); err != nil {
		return fmt.Errorf("discord send: %w", err)
	}
	return nil
}

// ChannelRef renders a Discord channel id as channel-mention markup so operator
// output links to the channel.
func (a *ChannelAdmin) ChannelRef(id string) string { return "<#" + id + ">" }

// Platform adapts the dctl client to the neutral channel ports
// contracts.ChannelReader and contracts.MenuRouter (the consumer's read/
// channel-bootstrap/reaction/status/routed-menu surface).
type Platform struct {
	c        *dctl.Client
	sinks    *sinks
	binds    *bindStore
	readImpl func(ctx context.Context, channelID string, limit int, after string) ([]rawMsg, error)
}

type rawMsg struct {
	id  string
	bot bool
	msg contracts.Message
}

// NewPlatform returns a Platform that works through the dctl client c.
func NewPlatform(c *dctl.Client) *Platform {
	platform := &Platform{c: c}
	platform.readImpl = platform.readDctl
	return platform
}

// Enabled reports whether the underlying dctl client is enabled.
func (p *Platform) Enabled() bool { return p.c.Enabled() }

// DefaultChannel returns the default channel id of the underlying dctl client.
func (p *Platform) DefaultChannel() string { return p.c.DefaultChannel() }

// EnsureChannel returns the channel named name, created under parentID when parentID
// is set and at the top level otherwise. It returns an error that wraps the Discord
// failure.
func (p *Platform) EnsureChannel(ctx context.Context, parentID, name string) (contracts.Channel, error) {
	ensure := func() (*dctl.Channel, error) {
		if parentID != "" {
			return p.c.Channels().EnsureUnder(ctx, parentID, name)
		}
		return p.c.Channels().Ensure(ctx, "", name)
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

func (p *Platform) readDctl(ctx context.Context, channelID string, limit int, after string) ([]rawMsg, error) {
	msgs, err := p.c.Messages().Read(ctx, channelID, limit, after)
	if err != nil {
		return nil, fmt.Errorf("discord read: %w", err)
	}
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
	return out, nil
}

// Read returns recent channel messages and records the id of the last non-bot
// message so the next turn's ACK reaction lands on it. A channel the router
// drives by push returns nothing: that channel already has an inbound path, and
// two would deliver every message twice.
func (p *Platform) Read(ctx context.Context, channelID string, limit int, after string) ([]contracts.Message, error) {
	if p.binds != nil && p.binds.Session(channelID) != "" {
		return nil, nil
	}
	raws, err := p.readImpl(ctx, channelID, limit, after)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.Message, 0, len(raws))
	for i := range raws {
		raw := raws[i]
		out = append(out, raw.msg)
		if !raw.bot && p.sinks != nil {
			p.sinks.at(raw.msg.ChannelID).noteUser(raw.msg.ChannelID, raw.id, raw.msg.AuthorID)
		}
	}
	return out, nil
}

// Unreact removes the emoji from the message messageID in channelID. It returns an
// error that wraps the Discord failure.
func (p *Platform) Unreact(ctx context.Context, channelID, messageID, emoji string) error {
	if err := p.c.Reactions().Remove(ctx, channelID, messageID, emoji); err != nil {
		return fmt.Errorf("discord unreact: %w", err)
	}
	return nil
}

// UpsertStatusMessage edits the message messageID in channelID to content. When
// messageID is empty, or Discord no longer has that message, it posts content as a
// new message. It returns the id of the message it edited or posted, and an error
// that wraps the Discord failure.
func (p *Platform) UpsertStatusMessage(ctx context.Context, channelID, messageID, content string) (string, error) {
	statusID, err := p.c.Interactions().UpsertStatusMessage(ctx, channelID, messageID, content)
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
	sent, err := p.c.Components().SendSelectMenu(ctx, channelID, replyTo, prompt, ChoiceCustomID(route), out)
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

func (r renderAdapter) Post(ctx context.Context, ch, content string) error {
	if _, err := r.p.c.Messages().Send(ctx, ch, content); err != nil {
		return fmt.Errorf("discord post: %w", err)
	}
	return nil
}

func (r renderAdapter) React(ctx context.Context, ch, id, emoji string) error {
	if err := r.p.c.Reactions().Add(ctx, ch, id, emoji); err != nil {
		return fmt.Errorf("discord react: %w", err)
	}
	return nil
}

func (r renderAdapter) Delete(ctx context.Context, ch, id string) error {
	if err := r.p.c.Messages().Delete(ctx, ch, id); err != nil {
		return fmt.Errorf("discord delete: %w", err)
	}
	return nil
}

var _ renderClient = (*renderAdapter)(nil)

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
