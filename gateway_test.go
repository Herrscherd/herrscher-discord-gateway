package discord

import (
	"context"
	"fmt"
	"testing"

	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts"
)

// outMsg and outMenu record where an outbound call landed, not just what it
// said: the router's whole job is choosing the right channel and custom_id.
type outMsg struct{ channel, content string }

type outMenu struct{ channel, content, customID string }

type fakeClient struct {
	sent    []outMsg
	replied []outMsg
	reacted []string
	menus   []outMenu
	read    []dctl.Message

	threads      []outMsg // channel the thread was opened in, and its name
	members      []outMsg // thread id, and the user added to it
	public       []outMsg // message a public thread was started on, and its name
	threadErr    error    // fails CreatePrivateThread
	publicErr    error    // fails StartThread
	memberErr    error    // fails AddThreadMember
	nextThreadID string
	started      int // public threads opened so far, so each gets its own id

	got    []outMsg      // channel and id of every GetMessage call
	getMsg *dctl.Message // what GetMessage answers
	getErr error         // fails GetMessage
}

func (f *fakeClient) Send(_ context.Context, ch, content string) (*dctl.Message, error) {
	f.sent = append(f.sent, outMsg{ch, content})
	return &dctl.Message{ID: "m1"}, nil
}
func (f *fakeClient) Reply(_ context.Context, ch, _, content string) (*dctl.Message, error) {
	f.replied = append(f.replied, outMsg{ch, content})
	return &dctl.Message{ID: "m2"}, nil
}
func (f *fakeClient) React(_ context.Context, _, _, emoji string) error {
	f.reacted = append(f.reacted, emoji)
	return nil
}
func (f *fakeClient) SendSelectMenu(_ context.Context, ch, _, content, customID string, _ []dctl.SelectOption) (*dctl.Message, error) {
	f.menus = append(f.menus, outMenu{ch, content, customID})
	return &dctl.Message{ID: "m3"}, nil
}
func (f *fakeClient) ReadMessages(context.Context, string, int, string) ([]dctl.Message, error) {
	return f.read, nil
}
func (f *fakeClient) GetMessage(_ context.Context, ch, id string) (*dctl.Message, error) {
	f.got = append(f.got, outMsg{ch, id})
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getMsg, nil
}
func (f *fakeClient) CreatePrivateThread(_ context.Context, ch, name string) (string, error) {
	if f.threadErr != nil {
		return "", f.threadErr
	}
	f.threads = append(f.threads, outMsg{ch, name})
	if f.nextThreadID == "" {
		return "thread1", nil
	}
	return f.nextThreadID, nil
}
func (f *fakeClient) StartThread(_ context.Context, ch, msg, name string) (string, error) {
	if f.publicErr != nil {
		return "", f.publicErr
	}
	f.public = append(f.public, outMsg{msg, name})
	f.started++
	return fmt.Sprintf("%s-t%d", ch, f.started), nil
}
func (f *fakeClient) AddThreadMember(_ context.Context, thread, user string) error {
	if f.memberErr != nil {
		return f.memberErr
	}
	f.members = append(f.members, outMsg{thread, user})
	return nil
}

var _ contracts.Gateway = (*Gateway)(nil)

func TestGatewayManifest(t *testing.T) {
	g := NewGateway(&fakeClient{})
	m := g.Manifest()
	if m.Kind != "discord" || m.Category != contracts.CategoryGateway {
		t.Fatalf("bad manifest %+v", m)
	}
	if !m.Capabilities.Reactions || !m.Capabilities.SelectMenus || !m.Capabilities.Replies {
		t.Fatalf("discord should announce all capabilities: %+v", m.Capabilities)
	}
	// Undeclared, the host's attachment allowlist is empty and every screenshot
	// is refused before it is fetched — silently, since a dropped attachment is
	// never worth failing a turn over.
	hosts := map[string]bool{}
	for _, h := range m.AttachmentHosts {
		hosts[h] = true
	}
	if !hosts["cdn.discordapp.com"] || !hosts["media.discordapp.net"] {
		t.Fatalf("manifest attachment hosts = %v, want the Discord CDN", m.AttachmentHosts)
	}
}

func TestGatewayTranslatesActions(t *testing.T) {
	fc := &fakeClient{}
	g := NewGateway(fc)
	ctx := context.Background()
	conv := contracts.Conversation{Gateway: "discord", ID: "chan"}

	_, _ = g.Post(ctx, conv, "hello")
	_, _ = g.Reply(ctx, conv, "mid", "answer")
	_ = g.React(ctx, conv, "mid", "👀")
	_ = g.Menu(ctx, conv, "mid", "pick", []contracts.Choice{{Label: "A", Value: "a"}})

	if len(fc.sent) != 1 || len(fc.replied) != 1 || len(fc.reacted) != 1 || len(fc.menus) != 1 {
		t.Fatalf("translation incomplete: %+v", fc)
	}
}

func TestGatewayImplementsEventSink(t *testing.T) {
	var _ contracts.EventSink = (*Gateway)(nil)
}

func TestGatewayEmitToForwardsToTheConversationSink(t *testing.T) {
	f := &fakeRender{}
	g := NewGateway(&fakeClient{})
	g.sinks = newSinks(context.Background(), f, staticLevels("full"), "", false)
	conv := contracts.Conversation{ID: "c1"}
	g.EmitTo(conv, contracts.Event{T: "human"})
	g.EmitTo(conv, contracts.Event{T: "reply", Text: "ok", Done: true})
	if got := f.postsTo("c1"); len(got) != 1 || got[0] != "ok" {
		t.Fatalf("posts = %v, want [ok]", got)
	}
}
