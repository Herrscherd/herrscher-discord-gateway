package discord

import (
	"context"
	"fmt"
	"time"

	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts"
)

// Discord channel-type ints (GUILD_CATEGORY is 4; dctl exports ChannelForum=15).
const channelCategory = 4

// ChannelAdmin adapts the dctl client to contracts.ChannelAdmin: session channel
// creation/archival and posting.
type ChannelAdmin struct{ c *dctl.Client }

func NewChannelAdmin(c *dctl.Client) *ChannelAdmin { return &ChannelAdmin{c: c} }

func (a *ChannelAdmin) Kind(ctx context.Context, id string) (string, error) {
	t, err := a.c.Channels().Type(ctx, id)
	if err != nil {
		return "", fmt.Errorf("discord channel type: %w", err)
	}
	switch t {
	case channelCategory:
		return "category", nil
	case dctl.ChannelForum:
		return "forum", nil
	default:
		return "", nil
	}
}

func (a *ChannelAdmin) CreateUnder(ctx context.Context, parentID, name string) (string, error) {
	ch, err := a.c.Channels().CreateUnder(ctx, parentID, name)
	if err != nil {
		return "", fmt.Errorf("discord create channel: %w", err)
	}
	if ch == nil {
		return "", nil
	}
	return ch.ID, nil
}

func (a *ChannelAdmin) ForumPost(ctx context.Context, forumID, name, content string) (string, error) {
	ch, err := a.c.Threads().ForumPost(ctx, forumID, name, content)
	if err != nil {
		return "", fmt.Errorf("discord forum post: %w", err)
	}
	if ch == nil {
		return "", nil
	}
	return ch.ID, nil
}

func (a *ChannelAdmin) Archive(ctx context.Context, id string) error {
	if err := a.c.Channels().Archive(ctx, id); err != nil {
		return fmt.Errorf("discord archive channel: %w", err)
	}
	return nil
}

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
	c     *dctl.Client
	sinks *sinks
	// binds tells which channels the router already drives by push; those are
	// skipped by Read (see there).
	binds    *bindStore
	readImpl func(ctx context.Context, channelID string, limit int, after string) ([]rawMsg, error)
}

// rawMsg is the minimal shape Read needs to track the last user message.
type rawMsg struct {
	id  string
	bot bool
	msg contracts.Message
}

func NewPlatform(c *dctl.Client) *Platform {
	p := &Platform{c: c}
	p.readImpl = p.readDctl
	return p
}

func (p *Platform) Enabled() bool          { return p.c.Enabled() }
func (p *Platform) DefaultChannel() string { return p.c.DefaultChannel() }

func (p *Platform) EnsureChannel(ctx context.Context, parentID, name string) (contracts.Channel, error) {
	// With a parent category, ensure the channel under it; otherwise ensure a
	// top-level text channel in the sole guild.
	ensure := func() (*dctl.Channel, error) {
		if parentID != "" {
			return p.c.Channels().EnsureUnder(ctx, parentID, name)
		}
		return p.c.Channels().Ensure(ctx, "", name)
	}
	ch, err := ensure()
	if err != nil {
		return contracts.Channel{}, fmt.Errorf("discord ensure channel: %w", err)
	}
	if ch == nil {
		return contracts.Channel{}, nil
	}
	return contracts.Channel{ID: ch.ID, Name: ch.Name}, nil
}

// readDctl is the production read seam: it pulls messages via dctl and adapts
// them to rawMsg (carrying the fully-mapped contracts.Message).
func (p *Platform) readDctl(ctx context.Context, channelID string, limit int, after string) ([]rawMsg, error) {
	msgs, err := p.c.Messages().Read(ctx, channelID, limit, after)
	if err != nil {
		return nil, fmt.Errorf("discord read: %w", err)
	}
	out := make([]rawMsg, 0, len(msgs))
	for _, m := range msgs {
		atts := make([]contracts.Attachment, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			atts = append(atts, contracts.Attachment{
				Filename:    a.Filename,
				URL:         a.URL,
				ContentType: a.ContentType,
				Size:        a.Size,
			})
		}
		out = append(out, rawMsg{
			id:  m.ID,
			bot: m.Author.Bot,
			msg: contracts.Message{
				ID:          m.ID,
				ChannelID:   m.ChannelID,
				Content:     m.Content,
				AuthorID:    m.Author.ID,
				AuthorName:  m.Author.Username,
				AuthorBot:   m.Author.Bot,
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
	for _, r := range raws {
		out = append(out, r.msg)
		if !r.bot && p.sinks != nil {
			// Newest non-bot id wins (messages are oldest→newest), recorded on the
			// sink of the channel the message actually came from.
			p.sinks.at(r.msg.ChannelID).noteUser(r.msg.ChannelID, r.id)
		}
	}
	return out, nil
}

func (p *Platform) Unreact(ctx context.Context, channelID, messageID, emoji string) error {
	if err := p.c.Reactions().Remove(ctx, channelID, messageID, emoji); err != nil {
		return fmt.Errorf("discord unreact: %w", err)
	}
	return nil
}

func (p *Platform) UpsertStatusMessage(ctx context.Context, channelID, messageID, content string) (string, error) {
	id, err := p.c.Interactions().UpsertStatusMessage(ctx, channelID, messageID, content)
	if err != nil {
		return id, fmt.Errorf("discord status message: %w", err)
	}
	return id, nil
}

func (p *Platform) RouteMenu(ctx context.Context, channelID, replyTo, prompt, route string, opts []contracts.Choice) (contracts.MessageID, error) {
	out := make([]dctl.SelectOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, dctl.SelectOption{Label: o.Label, Value: o.Value})
	}
	m, err := p.c.Components().SendSelectMenu(ctx, channelID, replyTo, prompt, ChoiceCustomID(route), out)
	if err != nil {
		return "", fmt.Errorf("discord menu: %w", err)
	}
	if m == nil {
		return "", nil
	}
	return contracts.MessageID(m.ID), nil
}

// renderAdapter exposes the exact renderClient surface the sink needs, backed by
// the dctl client. UpsertStatusMessage/Unreact reuse Platform's logic;
// Post/React go straight to dctl. Every method takes its channel, so one adapter
// serves every conversation's sink.
type renderAdapter struct{ p *Platform }

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

func NewProber(c *dctl.Client) *Prober { return &Prober{c: c} }

func (p *Prober) Probe(ctx context.Context) (int64, error) {
	start := time.Now()
	_, err := p.c.Interactions().AppID(ctx)
	if err != nil {
		return time.Since(start).Milliseconds(), fmt.Errorf("discord probe: %w", err)
	}
	return time.Since(start).Milliseconds(), nil
}

// Compile-time proof the Discord adapters satisfy the neutral channel ports.
var (
	_ contracts.ChannelReader = (*Platform)(nil)
	_ contracts.MenuRouter    = (*Platform)(nil)
	_ contracts.ChannelAdmin  = (*ChannelAdmin)(nil)
)
