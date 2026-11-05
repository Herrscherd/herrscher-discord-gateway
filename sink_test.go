package discord

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestChunkTextRuneSafe(t *testing.T) {
	input := strings.Repeat("…", gatewayMaxLen+10)
	parts := chunkText(input, gatewayMaxLen)
	for i, part := range parts {
		if !utf8.ValidString(part) {
			t.Fatalf("part %d is not valid UTF-8: %q", i, part)
		}
		if n := utf8.RuneCountInString(part); n > gatewayMaxLen {
			t.Fatalf("part %d has %d runes, want <= %d", i, n, gatewayMaxLen)
		}
	}
	if got := strings.Join(parts, ""); got != input {
		t.Fatalf("rejoined chunks lost runes: got %d runes, want %d",
			utf8.RuneCountInString(got), utf8.RuneCountInString(input))
	}
}

type fakeRender struct {
	channel   string
	upserts   []string
	posts     []string
	reacted   []string
	unreacted []string
	deleted   []msgRef
	deleteErr error
	statusID  string

	postChans []string
}

func (f *fakeRender) UpsertStatusMessage(_ context.Context, _, _, content string) (string, error) {
	f.upserts = append(f.upserts, content)
	if f.statusID == "" {
		f.statusID = "status1"
	}
	return f.statusID, nil
}

func (f *fakeRender) Post(_ context.Context, ch, content string) error {
	f.posts = append(f.posts, content)
	f.postChans = append(f.postChans, ch)
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

func (f *fakeRender) Delete(_ context.Context, ch, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, msgRef{ch: ch, id: id})
	return nil
}

func (f *fakeRender) postsTo(ch string) []string {
	var out []string
	for i, c := range f.postChans {
		if c == ch {
			out = append(out, f.posts[i])
		}
	}
	return out
}

func newTestSink(f *fakeRender) *sink {
	ch := f.channel
	if ch == "" {
		ch = "c1"
	}
	return newSink(testRun(), f, ch, staticLevel("full"), "", false, testMessages(), testLog())
}

func TestSinkDefaultsToSilent(t *testing.T) {
	if s := newSink(testRun(), &fakeRender{}, "c1", staticLevel(""), "", false, testMessages(), testLog()); s.level() != levelSilent {
		t.Fatalf("default level = %q, want %q", s.level(), levelSilent)
	}
}

func TestSinkLevelIsResolvedPerTurn(t *testing.T) {
	level := levelSilent
	s := newSink(testRun(), &fakeRender{channel: "c1"}, "c1", func() string { return level }, "", false, testMessages(), testLog())

	s.handle(contracts.Event{T: "human"})
	if s.pv != nil {
		t.Fatal("a silent conversation opened a live progress view")
	}
	level = levelFull
	s.handle(contracts.Event{T: "human"})
	if s.pv == nil {
		t.Fatal("the retuned level did not reach the next turn")
	}
}

func TestSilentLevelPostsOnlyTheAnswer(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(testRun(), f, "c1", staticLevel(levelSilent), "", false, testMessages(), testLog())
	s.noteUser(context.Background(), "c1", "u1", "42")

	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "status", Text: "Bash go test ./..."})
	s.handle(contracts.Event{T: "chunk", Text: "je regarde"})
	s.handle(contracts.Event{T: "reply", Text: "c'est corrigé", Done: true, Cost: 0.4})

	if len(f.upserts) != 0 {
		t.Fatalf("upserts = %v, want no progress message at all", f.upserts)
	}
	if len(f.posts) != 1 || f.posts[0] != "c'est corrigé" {
		t.Fatalf("posts = %v, want only the answer", f.posts)
	}
	if len(f.reacted) != 1 || len(f.unreacted) != 1 {
		t.Fatalf("reacted = %v, unreacted = %v, want the ack raised and cleared", f.reacted, f.unreacted)
	}
}

func TestSilentLevelClearsTheAckOnAbandon(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(testRun(), f, "c1", staticLevel(levelSilent), "", false, testMessages(), testLog())
	s.noteUser(context.Background(), "c1", "u1", "42")
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "status", Text: "Read x"})
	s.handle(contracts.Event{T: "abandoned"})

	if len(f.unreacted) != 1 || f.unreacted[0] != ackEmoji {
		t.Fatalf("unreacted = %v, want the ack cleared", f.unreacted)
	}
	if len(f.upserts) != 0 || len(f.posts) != 0 {
		t.Fatalf("upserts = %v, posts = %v, want nothing rendered", f.upserts, f.posts)
	}
}

func TestReplyPingsTheOwner(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(testRun(), f, "c1", staticLevel(levelSilent), "42", false, testMessages(), testLog())
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "reply", Text: "c'est corrigé", Done: true})

	if len(f.posts) != 1 || f.posts[0] != "<@42> c'est corrigé" {
		t.Fatalf("posts = %v, want the answer prefixed with the owner mention", f.posts)
	}
}

func TestReplyPingKeepsChunksWithinTheLimit(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(testRun(), f, "c1", staticLevel(levelSilent), "42", false, testMessages(), testLog())
	s.handle(contracts.Event{T: "reply", Text: strings.Repeat("x", gatewayMaxLen), Done: true})

	for i, p := range f.posts {
		if n := utf8.RuneCountInString(p); n > gatewayMaxLen {
			t.Fatalf("post %d has %d runes, want <= %d", i, n, gatewayMaxLen)
		}
	}
	if !strings.HasPrefix(f.posts[0], "<@42> ") {
		t.Fatalf("first post = %q, want it to open with the mention", f.posts[0][:20])
	}
}

func TestEmptyReplyStillAnswers(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(testRun(), f, "c1", staticLevel(levelSilent), "42", false, testMessages(), testLog())
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "reply", Done: true})

	if len(f.posts) != 1 || !strings.Contains(f.posts[0], "done") {
		t.Fatalf("posts = %v, want one completion line", f.posts)
	}
}

func TestSinksRenderPerConversationIndependently(t *testing.T) {
	f := &fakeRender{}
	set := newSinks(testRun(), f, staticLevels("full"), "", false, testMessages(), testLog())

	set.at("chanA").noteUser(context.Background(), "chanA", "mA", "42")
	set.at("chanB").noteUser(context.Background(), "chanB", "mB", "42")
	set.at("chanA").handle(contracts.Event{T: "human"})
	set.at("chanB").handle(contracts.Event{T: "human"})
	set.at("chanA").handle(contracts.Event{T: "reply", Text: "answer A", Done: true})

	if got := f.postsTo("chanB"); len(got) != 0 {
		t.Fatalf("conversation B received %v; A's reply leaked across channels", got)
	}
	if got := f.postsTo("chanA"); len(got) != 1 || got[0] != "answer A" {
		t.Fatalf("conversation A posts = %v, want [answer A]", got)
	}
	first := set.at("chanA")
	if first != set.at("chanA") {
		t.Fatal("at() returned a fresh sink for a known conversation, per-turn state would be lost")
	}
}

func TestGatewayEmitToRoutesByConversation(t *testing.T) {
	f := &fakeRender{}
	g := &Gateway{sinks: newSinks(testRun(), f, staticLevels("full"), "", false, testMessages(), testLog())}

	g.EmitTo(contracts.Conversation{ID: "chanA"}, contracts.Event{T: "reply", Text: "hello A", Done: true})
	g.Emit(contracts.Event{T: "reply", Text: "orphan", Done: true})

	if got := f.postsTo("chanA"); len(got) != 1 || got[0] != "hello A" {
		t.Fatalf("chanA posts = %v, want [hello A]", got)
	}
	if len(f.posts) != 1 {
		t.Fatalf("posts = %v, want only the routed one", f.posts)
	}
}

func TestSinkAcksHumanAndSummarizesReply(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.noteUser(context.Background(), "c1", "u1", "42")

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

func TestSinkAbandonedClearsAck(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.noteUser(context.Background(), "c1", "u1", "42")
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "status", Text: "Read x"})
	s.handle(contracts.Event{T: "status", Text: "Edit y"})
	s.handle(contracts.Event{T: "abandoned"})

	if len(f.unreacted) != 1 || f.unreacted[0] != ackEmoji {
		t.Fatalf("unreacted = %v, want one %q on abandon", f.unreacted, ackEmoji)
	}
	if len(f.posts) != 0 {
		t.Fatalf("posts = %v, want none on abandon", f.posts)
	}
	last := f.upserts[len(f.upserts)-1]
	if strings.HasPrefix(last, "✅") {
		t.Fatalf("abandoned status = %q, want no ✅ summary", last)
	}
	if !strings.Contains(last, "Edit · y") {
		t.Fatalf("abandoned status = %q, want it to include the coalesced line", last)
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

func TestSinkResetDiscardsAndContinues(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.noteUser(context.Background(), "c1", "u1", "42")
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "status", Text: "Read x"})
	s.handle(contracts.Event{T: "reset"})

	afterReset := f.upserts[len(f.upserts)-1]
	if strings.HasPrefix(afterReset, "✅") || strings.HasPrefix(afterReset, "⚠️") {
		t.Fatalf("reset status = %q, want in-progress (no summary)", afterReset)
	}
	if len(f.unreacted) != 0 {
		t.Fatalf("unreacted = %v, want ACK kept during retry", f.unreacted)
	}

	s.handle(contracts.Event{T: "status", Text: "Edit y"})
	s.handle(contracts.Event{T: "reply", Text: "done", Done: true, Cost: 0.01})

	last := f.upserts[len(f.upserts)-1]
	if !strings.HasPrefix(last, "✅") {
		t.Fatalf("final status = %q, want ✅ summary", last)
	}
	if strings.Contains(last, "2 action") {
		t.Fatalf("final status = %q, want only post-reset action counted", last)
	}
	if len(f.unreacted) != 1 || f.unreacted[0] != ackEmoji {
		t.Fatalf("unreacted = %v, want one %q at turn end", f.unreacted, ackEmoji)
	}
}
