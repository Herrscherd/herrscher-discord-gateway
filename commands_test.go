package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

func cmdNamed(t *testing.T, g *Gateway, path ...string) contracts.Cmd {
	t.Helper()
	want := strings.Join(path, " ")
	for _, c := range g.Commands() {
		if strings.Join(c.Path, " ") == want {
			return c
		}
	}
	t.Fatalf("no command %q", want)
	return contracts.Cmd{}
}

// The gateway contributes its verbs, and it declares them unprefixed: the host
// adds the plugin's kind. A path already spelling "discord" here would come out
// as "discord discord channel read".
func TestCommandsAreDeclaredUnprefixed(t *testing.T) {
	var _ contracts.CommandSource = (*Gateway)(nil)

	g := NewGateway(&fakeClient{})
	for _, c := range g.Commands() {
		if c.Path[0] == "discord" {
			t.Fatalf("the host owns the prefix; %v must not carry it", c.Path)
		}
	}
	if len(g.Commands()) != 7 {
		t.Fatalf("seven verbs are contributed, got %d", len(g.Commands()))
	}
}

// A read renders every message with its author and its id, so an agent can both
// read it and repaginate with --after.
func TestChannelReadRendersAuthorsAndIDs(t *testing.T) {
	f := &fakeClient{read: []dctl.Message{
		{ID: "1", Content: "hello", Author: dctl.Author{ID: "u1", Username: "ana"}},
		{ID: "2", Content: "hi", Author: dctl.Author{ID: "u2", Username: "bo", Bot: true}},
	}}
	g := NewGateway(f)
	out, err := cmdNamed(t, g, "channel", "read").Run(context.Background(),
		contracts.Input{Args: map[string]string{"id": "c1"}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"ana", "hello", "1", "bo", "hi", "2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("read output must contain %q: %q", want, out)
		}
	}
}

// Bot messages survive. The poller drops them because it is manufacturing turns;
// an explicit read must show the channel as it is, the bot's own answers
// included — that is frequently the thread worth re-reading.
func TestChannelReadKeepsBotMessages(t *testing.T) {
	f := &fakeClient{read: []dctl.Message{{ID: "2", Content: "an answer", Author: dctl.Author{ID: "u3", Username: "bot", Bot: true}}}}
	g := NewGateway(f)
	out, _ := cmdNamed(t, g, "channel", "read").Run(context.Background(),
		contracts.Input{Args: map[string]string{"id": "c1"}})
	if !strings.Contains(out, "an answer") {
		t.Fatalf("a bot message must survive an explicit read: %q", out)
	}
}

// The cap is enforced on the way in. An agent asking for five thousand messages
// would drown its own context and take the platform's rate limit.
func TestChannelReadCapsTheLimit(t *testing.T) {
	f := &fakeClient{}
	g := NewGateway(f)
	if _, err := cmdNamed(t, g, "channel", "read").Run(context.Background(),
		contracts.Input{Args: map[string]string{"id": "c1", "limit": "5000"}}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if f.readLimit != readCap {
		t.Fatalf("the limit must be capped at %d, asked %d", readCap, f.readLimit)
	}
	// A garbage limit falls back to the cap rather than to zero: a typo must not
	// silently read nothing.
	f.readLimit = 0
	if _, err := cmdNamed(t, g, "channel", "read").Run(context.Background(),
		contracts.Input{Args: map[string]string{"id": "c1", "limit": "abc"}}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if f.readLimit != readCap {
		t.Fatalf("an unparseable limit must fall back to %d, got %d", readCap, f.readLimit)
	}
}

// An empty channel says so. Returning nothing at all reads as a broken command.
func TestChannelReadSaysWhenEmpty(t *testing.T) {
	g := NewGateway(&fakeClient{})
	out, err := cmdNamed(t, g, "channel", "read").Run(context.Background(),
		contracts.Input{Args: map[string]string{"id": "c1"}})
	if err != nil {
		t.Fatalf("an empty channel is not an error: %v", err)
	}
	if out == "" {
		t.Fatal("an empty read must say so rather than return nothing")
	}
}

// Each write verb reaches the client with what it was given.
func TestWriteCommandsReachTheClient(t *testing.T) {
	f := &fakeClient{}
	g := NewGateway(f)
	ctx := context.Background()

	if _, err := cmdNamed(t, g, "channel", "post").Run(ctx,
		contracts.Input{Args: map[string]string{"id": "c1", "text": "yo"}}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if len(f.sent) != 1 || f.sent[0].content != "yo" {
		t.Fatalf("post must reach the client: %v", f.sent)
	}

	if _, err := cmdNamed(t, g, "message", "delete").Run(ctx,
		contracts.Input{Args: map[string]string{"id": "c1", "msg": "m1"}}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(f.deleted) != 1 {
		t.Fatalf("delete must reach the client: %v", f.deleted)
	}

	if _, err := cmdNamed(t, g, "message", "edit").Run(ctx,
		contracts.Input{Args: map[string]string{"id": "c1", "msg": "m1", "text": "fixed"}}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if len(f.edited) != 1 || f.edited[0].content != "fixed" {
		t.Fatalf("edit must reach the client: %v", f.edited)
	}
}

// A missing required argument is refused by the registry's own validation, so
// the handler never runs with an empty channel id.
func TestRequiredArgsAreDeclared(t *testing.T) {
	g := NewGateway(&fakeClient{})
	c := cmdNamed(t, g, "channel", "read")
	var found bool
	for _, p := range c.Params {
		if p.Name == "id" {
			found = p.Required
		}
	}
	if !found {
		t.Fatal("the channel id must be declared required")
	}
}

// The remaining verbs reach the client too, unreact included — it is the one
// call the narrow client interface did not carry before.
func TestReactVerbsReachTheClient(t *testing.T) {
	f := &fakeClient{}
	g := NewGateway(f)
	ctx := context.Background()

	if _, err := cmdNamed(t, g, "message", "reply").Run(ctx,
		contracts.Input{Args: map[string]string{"id": "c1", "to": "m1", "text": "sure"}}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if len(f.replied) != 1 || f.replied[0].content != "sure" {
		t.Fatalf("reply must reach the client: %v", f.replied)
	}

	if _, err := cmdNamed(t, g, "message", "react").Run(ctx,
		contracts.Input{Args: map[string]string{"id": "c1", "msg": "m1", "emoji": "👀"}}); err != nil {
		t.Fatalf("react: %v", err)
	}
	if len(f.reacted) != 1 || f.reacted[0] != "👀" {
		t.Fatalf("react must reach the client: %v", f.reacted)
	}

	if _, err := cmdNamed(t, g, "message", "unreact").Run(ctx,
		contracts.Input{Args: map[string]string{"id": "c1", "msg": "m1", "emoji": "👀"}}); err != nil {
		t.Fatalf("unreact: %v", err)
	}
	if len(f.unreacted) != 1 || f.unreacted[0] != "👀" {
		t.Fatalf("unreact must reach the client: %v", f.unreacted)
	}
}
