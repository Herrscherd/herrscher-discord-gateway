package discord

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

type outMsg struct{ channel, content string }

type outMenu struct{ channel, content, customID string }

type outEdit struct{ channel, id, content string }

type outReply struct{ channel, to, content string }

type outReact struct{ channel, msg, emoji string }

type fakeClient struct {
	sent       []outMsg
	replied    []outReply
	reacted    []outReact
	menus      []outMenu
	menuErr    error
	sendErr    error
	sendErrAt  int
	sends      int
	read       []dctl.Message
	readLimit  int
	readErr    error
	unreacted  []outReact
	reactErr   error
	unreactErr error

	threads      []outMsg
	members      []outMsg
	public       []outMsg
	threadErr    error
	publicErr    error
	memberErr    error
	nextThreadID string
	started      int

	got    []outMsg
	getMsg *dctl.Message
	getErr error

	edited          []outEdit
	deleted         []string
	deletedChannels []string
	deleteChanErr   error

	trace      []string
	fileSends  int
	filesErr   error
	filesErrAt int
}

func (f *fakeClient) Send(_ context.Context, ch, content string) (*dctl.Message, error) {
	f.sends++
	if f.sendErr != nil && (f.sendErrAt == 0 || f.sendErrAt == f.sends) {
		return nil, f.sendErr
	}
	f.sent = append(f.sent, outMsg{ch, content})
	f.trace = append(f.trace, fmt.Sprintf("send %s %q", ch, content))
	return &dctl.Message{ID: "m1"}, nil
}

func (f *fakeClient) Reply(_ context.Context, ch, to, content string) (*dctl.Message, error) {
	f.replied = append(f.replied, outReply{ch, to, content})
	f.trace = append(f.trace, fmt.Sprintf("reply %s %s %q", ch, to, content))
	return &dctl.Message{ID: "m2"}, nil
}

func (f *fakeClient) SendFiles(_ context.Context, ch, content string, files []dctl.File) (*dctl.Message, error) {
	return f.sendFiles(ch, "", content, files)
}

func (f *fakeClient) ReplyFiles(_ context.Context, ch, to, content string, files []dctl.File) (*dctl.Message, error) {
	return f.sendFiles(ch, to, content, files)
}

func (f *fakeClient) sendFiles(ch, to, content string, files []dctl.File) (*dctl.Message, error) {
	f.fileSends++
	if f.filesErr != nil && (f.filesErrAt == 0 || f.filesErrAt == f.fileSends) {
		return nil, f.filesErr
	}
	line, err := describeFileSend(ch, to, content, files)
	if err != nil {
		return nil, err
	}
	f.trace = append(f.trace, line)
	return &dctl.Message{ID: fmt.Sprintf("f%d", f.fileSends)}, nil
}

func describeFileSend(ch, to, content string, files []dctl.File) (string, error) {
	named := make([]string, 0, len(files))
	for _, file := range files {
		body, err := io.ReadAll(file.Content)
		if err != nil {
			return "", err
		}
		named = append(named, file.Name+"="+string(body))
	}
	if to == "" {
		return fmt.Sprintf("send-files %s %q [%s]", ch, content, strings.Join(named, " ")), nil
	}
	return fmt.Sprintf("reply-files %s %s %q [%s]", ch, to, content, strings.Join(named, " ")), nil
}

func (f *fakeClient) React(_ context.Context, ch, msg, emoji string) error {
	if f.reactErr != nil {
		return f.reactErr
	}
	f.reacted = append(f.reacted, outReact{ch, msg, emoji})
	return nil
}

func (f *fakeClient) SendSelectMenu(_ context.Context, ch, _, content, customID string, _ []dctl.SelectOption) (*dctl.Message, error) {
	if f.menuErr != nil {
		return nil, f.menuErr
	}
	f.menus = append(f.menus, outMenu{ch, content, customID})
	return &dctl.Message{ID: "m3"}, nil
}

func (f *fakeClient) ReadMessages(_ context.Context, _ string, limit int, _ string) ([]dctl.Message, error) {
	f.readLimit = limit
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.read, nil
}

func (f *fakeClient) Unreact(_ context.Context, ch, msg, emoji string) error {
	if f.unreactErr != nil {
		return f.unreactErr
	}
	f.unreacted = append(f.unreacted, outReact{ch, msg, emoji})
	return nil
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

func (f *fakeClient) EditMessage(_ context.Context, ch, id, content string) (*dctl.Message, error) {
	f.edited = append(f.edited, outEdit{ch, id, content})
	return &dctl.Message{ID: id}, nil
}

func (f *fakeClient) DeleteMessage(_ context.Context, ch, id string) error {
	f.deleted = append(f.deleted, ch+"/"+id)
	return nil
}

func (f *fakeClient) DeleteChannel(_ context.Context, id string) error {
	f.deletedChannels = append(f.deletedChannels, id)
	return f.deleteChanErr
}

var _ contracts.Gateway = (*Gateway)(nil)

func TestGatewayManifest(t *testing.T) {
	g := NewGateway(&fakeClient{}, testRun(), testMessages(), testLog())
	m := g.Manifest()
	if m.Kind != "discord" || m.Category != contracts.CategoryGateway {
		t.Fatalf("bad manifest %+v", m)
	}
	if !m.Capabilities.Reactions || !m.Capabilities.SelectMenus || !m.Capabilities.Replies {
		t.Fatalf("discord should announce all capabilities: %+v", m.Capabilities)
	}
	hosts := make(map[string]bool)
	for _, h := range m.AttachmentHosts {
		hosts[h] = true
	}
	if !hosts["cdn.discordapp.com"] || !hosts["media.discordapp.net"] {
		t.Fatalf("manifest attachment hosts = %v, want the Discord CDN", m.AttachmentHosts)
	}
}

func TestGatewayTranslatesActions(t *testing.T) {
	fc := &fakeClient{}
	g := NewGateway(fc, testRun(), testMessages(), testLog())
	ctx := context.Background()
	conv := contracts.Conversation{Gateway: "discord", ID: "chan"}

	_, err := g.Post(ctx, conv, "hello")
	requireNoError(t, err)
	_, err = g.Reply(ctx, conv, "mid", "answer")
	requireNoError(t, err)
	requireNoError(t, g.React(ctx, conv, "mid", "👀"))
	requireNoError(t, g.Menu(ctx, conv, "mid", "pick", []contracts.Choice{{Label: "A", Value: "a"}}))

	if len(fc.sent) != 1 || len(fc.replied) != 1 || len(fc.reacted) != 1 || len(fc.menus) != 1 {
		t.Fatalf("translation incomplete: %+v", fc)
	}
}

func TestGatewayEditsAndDeletes(t *testing.T) {
	var _ contracts.MessageEditor = (*Gateway)(nil)

	f := &fakeClient{}
	g := NewGateway(f, testRun(), testMessages(), testLog())
	if err := g.Edit(context.Background(), "c1", "m1", "fixed"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if len(f.edited) != 1 || f.edited[0] != (outEdit{"c1", "m1", "fixed"}) {
		t.Fatalf("edit must reach the client unchanged: %v", f.edited)
	}
	if err := g.Delete(context.Background(), "c1", "m1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.deleted) != 1 || f.deleted[0] != "c1/m1" {
		t.Fatalf("delete must reach the client unchanged: %v", f.deleted)
	}
}

func TestGatewayEmitToForwardsToTheConversationSink(t *testing.T) {
	f := &fakeRender{}
	g := NewGateway(&fakeClient{}, testRun(), testMessages(), testLog())
	g.sinks = newSinks(testRun(), f, staticLevels("full"), "", false, testMessages(), testLog())
	conv := contracts.Conversation{ID: "c1"}
	g.EmitTo(conv, contracts.Event{T: "human"})
	g.EmitTo(conv, contracts.Event{T: "reply", Text: "ok", Done: true})
	if got := f.postsTo("c1"); len(got) != 1 || got[0] != "ok" {
		t.Fatalf("posts = %v, want [ok]", got)
	}
}
