package discord

import (
	"context"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type fakeRender struct {
	channel   string
	upserts   []string // status contents in order
	posts     []string // final replies posted
	reacted   []string // emojis added
	unreacted []string // emojis removed
	statusID  string
}

func (f *fakeRender) DefaultChannel() string { return f.channel }
func (f *fakeRender) UpsertStatusMessage(_ context.Context, _, id, content string) (string, error) {
	f.upserts = append(f.upserts, content)
	if f.statusID == "" {
		f.statusID = "status1"
	}
	return f.statusID, nil
}
func (f *fakeRender) Post(_ context.Context, _, content string) error {
	f.posts = append(f.posts, content)
	return nil
}
func (f *fakeRender) React(_ context.Context, _, _, emoji string) error {
	f.reacted = append(f.reacted, emoji)
	return nil
}
func (f *fakeRender) Unreact(_ context.Context, _, _, emoji string) error {
	f.unreacted = append(f.unreacted, emoji)
	return nil
}

func newTestSink(f *fakeRender) *sink {
	s := newSink(context.Background(), f, "full")
	return s
}

func TestSinkAcksHumanAndSummarizesReply(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.noteUser("u1")

	s.handle(contracts.Event{T: "human", Who: "alice", Text: "hi"})
	if len(f.reacted) != 1 || f.reacted[0] != ackEmoji {
		t.Fatalf("reacted = %v, want one %q", f.reacted, ackEmoji)
	}
	s.handle(contracts.Event{T: "status", Text: "Read envfile.go"})
	s.handle(contracts.Event{T: "reply", Text: "done", Done: true, Cost: 0.02})

	if len(f.posts) != 1 || f.posts[0] != "done" {
		t.Fatalf("posts = %v, want [done]", f.posts)
	}
	if len(f.unreacted) != 1 || f.unreacted[0] != ackEmoji {
		t.Fatalf("unreacted = %v, want one %q", f.unreacted, ackEmoji)
	}
	last := f.upserts[len(f.upserts)-1]
	if !strings.HasPrefix(last, "✅") {
		t.Fatalf("final status = %q, want ✅ summary", last)
	}
}

func TestSinkChunksLongReply(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.handle(contracts.Event{T: "human"})
	long := strings.Repeat("x", gatewayMaxLen+50)
	s.handle(contracts.Event{T: "reply", Text: long, Done: true})
	if len(f.posts) != 2 {
		t.Fatalf("posts = %d chunks, want 2", len(f.posts))
	}
}

func TestSinkResetCollapsesProgress(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "status", Text: "Read x"})
	s.handle(contracts.Event{T: "reset"})
	last := f.upserts[len(f.upserts)-1]
	if !strings.HasPrefix(last, "⚠️") {
		t.Fatalf("reset status = %q, want ⚠️ summary", last)
	}
}
