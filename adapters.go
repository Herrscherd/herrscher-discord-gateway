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
// contracts.ChannelReader and contracts.MenuRouter (the bridge's read/
// channel-bootstrap/reaction/status/routed-menu surface).
type Platform struct{ c *dctl.Client }

func NewPlatform(c *dctl.Client) *Platform { return &Platform{c: c} }

func (p *Platform) Enabled() bool          { return p.c.Enabled() }
func (p *Platform) DefaultChannel() string { return p.c.DefaultChannel() }

func (p *Platform) EnsureChannel(ctx context.Context, parentID, name string) (contracts.Channel, error) {
	// dctl ensures a top-level text channel by name in the sole guild; parentID
	// has no analogue in the v1 client and is ignored.
	_ = parentID
	ch, err := p.c.Channels().Ensure(ctx, "", name)
	if err != nil {
		return contracts.Channel{}, err
	}
	if ch == nil {
		return contracts.Channel{}, nil
	}
	return contracts.Channel{ID: ch.ID, Name: ch.Name}, nil
}

func (p *Platform) Read(ctx context.Context, channelID string, limit int, after string) ([]contracts.Message, error) {
	msgs, err := p.c.Messages().Read(ctx, channelID, limit, after)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.Message, 0, len(msgs))
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
		out = append(out, contracts.Message{
			ID:          m.ID,
			ChannelID:   m.ChannelID,
			Content:     m.Content,
			AuthorID:    m.Author.ID,
			AuthorName:  m.Author.Username,
			AuthorBot:   m.Author.Bot,
			Attachments: atts,
		})
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

// responder is the per-command contracts.Responder. It packs the interaction
// id+token and absorbs Discord's interaction mechanics — the ack-then-edit defer
// dance for slow work, the autocomplete reply, and the component ack — so the
// host only declares neutral intent (slow yes/no, the choices, the ack text).
type responder struct {
	c          *dctl.Client
	appID      string
	id, token  string
}

func (r *responder) Respond(ctx context.Context, slow bool, produce func(context.Context) contracts.CommandResponse) error {
	if !slow {
		resp := produce(ctx)
		return r.c.Interactions().Respond(ctx, r.id, r.token, dctl.Response{Content: resp.Content, Ephemeral: resp.Private})
	}
	// Slow path: ack within the 3s callback deadline, run the work, then edit the
	// deferred reply in. On a defer failure, fall back to a direct reply so the
	// user is never left without a response.
	if err := r.c.Interactions().Defer(ctx, r.id, r.token, true); err != nil {
		resp := produce(ctx)
		return r.c.Interactions().Respond(ctx, r.id, r.token, dctl.Response{Content: resp.Content, Ephemeral: resp.Private})
	}
	resp := produce(ctx)
	return r.c.Interactions().EditResponse(ctx, r.appID, r.token, dctl.Response{Content: resp.Content, Ephemeral: resp.Private})
}

func (r *responder) Suggest(ctx context.Context, choices []contracts.Choice) error {
	out := make([]dctl.AutocompleteChoice, 0, len(choices))
	for _, ch := range choices {
		out = append(out, dctl.AutocompleteChoice{Name: ch.Label, Value: ch.Value})
	}
	return r.c.Interactions().RespondAutocomplete(ctx, r.id, r.token, out)
}

func (r *responder) AckPick(ctx context.Context, content string) error {
	return r.c.Components().Ack(ctx, r.id, r.token, content)
}

// Registrar adapts the slash-command registration to contracts.CommandRegistrar.
type Registrar struct{ c *dctl.Client }

func NewRegistrar(c *dctl.Client) *Registrar { return &Registrar{c: c} }

func (r *Registrar) Register(ctx context.Context) error { return RegisterCommands(ctx, r.c) }

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
	_ contracts.Responder     = (*responder)(nil)
)
