package discord

import (
	"context"
	"time"

	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts"
)

// Discord channel-type ints (GUILD_CATEGORY is 4; dctl exports ChannelForum=15).
const channelCategory = 4

// ChannelAdmin adapts the dctl client to serve.ChannelAdmin: session channel
// creation/archival and posting.
type ChannelAdmin struct{ c *dctl.Client }

func NewChannelAdmin(c *dctl.Client) *ChannelAdmin { return &ChannelAdmin{c: c} }

func (a *ChannelAdmin) Kind(ctx context.Context, id string) (string, error) {
	t, err := a.c.Channels().Type(ctx, id)
	if err != nil {
		return "", err
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
		return "", err
	}
	if ch == nil {
		return "", nil
	}
	return ch.ID, nil
}

func (a *ChannelAdmin) ForumPost(ctx context.Context, forumID, name, content string) (string, error) {
	ch, err := a.c.Threads().ForumPost(ctx, forumID, name, content)
	if err != nil {
		return "", err
	}
	if ch == nil {
		return "", nil
	}
	return ch.ID, nil
}

func (a *ChannelAdmin) Archive(ctx context.Context, id string) error {
	return a.c.Channels().Archive(ctx, id)
}

func (a *ChannelAdmin) Send(ctx context.Context, channelID, content string) error {
	_, err := a.c.Messages().Send(ctx, channelID, content)
	return err
}

// Platform adapts the dctl client to the neutral channel ports
// contracts.ChannelReader and contracts.MenuRouter (the consumer's read/
// channel-bootstrap/reaction/status/routed-menu surface).
type Platform struct {
	c        *dctl.Client
	sink     *sink
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
		return contracts.Channel{}, err
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
		return nil, err
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

func (p *Platform) Read(ctx context.Context, channelID string, limit int, after string) ([]contracts.Message, error) {
	raws, err := p.readImpl(ctx, channelID, limit, after)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.Message, 0, len(raws))
	for _, r := range raws {
		out = append(out, r.msg)
		if !r.bot && p.sink != nil {
			p.sink.noteUser(r.id) // newest non-bot id wins (messages are oldest→newest)
		}
	}
	return out, nil
}

func (p *Platform) Unreact(ctx context.Context, channelID, messageID, emoji string) error {
	return p.c.Reactions().Remove(ctx, channelID, messageID, emoji)
}

func (p *Platform) UpsertStatusMessage(ctx context.Context, channelID, messageID, content string) (string, error) {
	return p.c.Interactions().UpsertStatusMessage(ctx, channelID, messageID, content)
}

func (p *Platform) RouteMenu(ctx context.Context, channelID, replyTo, prompt, route string, opts []contracts.Choice) (contracts.MessageID, error) {
	out := make([]dctl.SelectOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, dctl.SelectOption{Label: o.Label, Value: o.Value})
	}
	m, err := p.c.Components().SendSelectMenu(ctx, channelID, replyTo, prompt, ChoiceCustomID(route), out)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", nil
	}
	return contracts.MessageID(m.ID), nil
}

// renderAdapter exposes the exact renderClient surface the sink needs, backed by
// the dctl client. DefaultChannel/UpsertStatusMessage/Unreact reuse Platform's
// logic; Post/React go straight to dctl.
type renderAdapter struct{ p *Platform }

func (r renderAdapter) DefaultChannel() string { return r.p.DefaultChannel() }
func (r renderAdapter) UpsertStatusMessage(ctx context.Context, ch, id, content string) (string, error) {
	return r.p.UpsertStatusMessage(ctx, ch, id, content)
}
func (r renderAdapter) Unreact(ctx context.Context, ch, id, emoji string) error {
	return r.p.Unreact(ctx, ch, id, emoji)
}
func (r renderAdapter) Post(ctx context.Context, ch, content string) error {
	_, err := r.p.c.Messages().Send(ctx, ch, content)
	return err
}
func (r renderAdapter) React(ctx context.Context, ch, id, emoji string) error {
	return r.p.c.Reactions().Add(ctx, ch, id, emoji)
}

var _ renderClient = (*renderAdapter)(nil)

// Prober adapts a cheap REST round-trip (/users/@me) to contracts.Prober.
type Prober struct{ c *dctl.Client }

func NewProber(c *dctl.Client) *Prober { return &Prober{c: c} }

func (p *Prober) Probe(ctx context.Context) (int64, error) {
	start := time.Now()
	_, err := p.c.Interactions().AppID(ctx)
	return time.Since(start).Milliseconds(), err
}

// Compile-time proof the Discord adapters satisfy the neutral channel ports.
var (
	_ contracts.ChannelReader = (*Platform)(nil)
	_ contracts.MenuRouter    = (*Platform)(nil)
	_ contracts.ChannelAdmin  = (*ChannelAdmin)(nil)
)
