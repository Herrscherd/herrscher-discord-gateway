package discord

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

// TestChunkTextRuneSafe proves chunkText never splits a multibyte rune and
// counts the limit in runes, not bytes (French text uses accented runes).
func TestChunkTextRuneSafe(t *testing.T) {
	// "…" is a 3-byte rune the app renders; a byte cut at 2000 (not a
	// multiple of 3) lands mid-rune, producing invalid UTF-8.
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
	upserts   []string // status contents in order
	posts     []string // final replies posted
	reacted   []string // emojis added
	unreacted []string // emojis removed
	deleted   []msgRef // messages deleted
	deleteErr error    // when set, every Delete fails
	statusID  string

	// postChans records the channel each post landed in, so a test can prove one
	// conversation's render never leaks into another's.
	postChans []string
}

func (f *fakeRender) UpsertStatusMessage(_ context.Context, _, id, content string) (string, error) {
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

// postsTo returns the contents posted into one channel, in order.
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
	return newSink(context.Background(), f, ch, staticLevel("full"), "", false)
}

// An unconfigured sink renders at the level that leaks nothing: the operator
// pings this bot in channels other people read.
func TestSinkDefaultsToSilent(t *testing.T) {
	if s := newSink(context.Background(), &fakeRender{}, "c1", staticLevel(""), "", false); s.level() != levelSilent {
		t.Fatalf("default level = %q, want %q", s.level(), levelSilent)
	}
}

// The level is read at the start of every turn, never captured when the sink was
// built: `/verbosity` has to take effect on the next turn, not on the next
// daemon restart, and a sink outlives every turn it renders.
func TestSinkLevelIsResolvedPerTurn(t *testing.T) {
	level := levelSilent
	s := newSink(context.Background(), &fakeRender{channel: "c1"}, "c1", func() string { return level }, "", false)

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

// At the silent level a turn is the ⏳ and then the answer: no live message, no
// ✅ summary. The operator asked to be pinged in public channels without a wall
// of tool calls following every request.
func TestSilentLevelPostsOnlyTheAnswer(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(context.Background(), f, "c1", staticLevel(levelSilent), "", false)
	s.noteUser("c1", "u1")

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
	// The ⏳ still says "received": it is the only sign the ping landed.
	if len(f.reacted) != 1 || len(f.unreacted) != 1 {
		t.Fatalf("reacted = %v, unreacted = %v, want the ack raised and cleared", f.reacted, f.unreacted)
	}
}

// An abandoned turn at the silent level still clears the ⏳, or the ping reads
// as pending forever.
func TestSilentLevelClearsTheAckOnAbandon(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(context.Background(), f, "c1", staticLevel(levelSilent), "", false)
	s.noteUser("c1", "u1")
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

// The answer pings the operator: a turn takes minutes, and its reply is a plain
// post that Discord notifies nobody about.
func TestReplyPingsTheOwner(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(context.Background(), f, "c1", staticLevel(levelSilent), "42", false)
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "reply", Text: "c'est corrigé", Done: true})

	if len(f.posts) != 1 || f.posts[0] != "<@42> c'est corrigé" {
		t.Fatalf("posts = %v, want the answer prefixed with the owner mention", f.posts)
	}
}

// The mention is part of the text before it is chunked, so the first chunk still
// fits Discord's limit.
func TestReplyPingKeepsChunksWithinTheLimit(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(context.Background(), f, "c1", staticLevel(levelSilent), "42", false)
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

// A turn that ends with nothing to say still says so. The alternative is the ⏳
// vanishing with no message, which reads as a bot that died mid-turn.
func TestEmptyReplyStillAnswers(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(context.Background(), f, "c1", staticLevel(levelSilent), "42", false)
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "reply", Done: true})

	if len(f.posts) != 1 || !strings.Contains(f.posts[0], "terminé") {
		t.Fatalf("posts = %v, want one completion line", f.posts)
	}
}

func TestSinksRenderPerConversationIndependently(t *testing.T) {
	f := &fakeRender{}
	set := newSinks(context.Background(), f, staticLevels("full"), "", false)

	set.at("chanA").noteUser("chanA", "mA")
	set.at("chanB").noteUser("chanB", "mB")
	set.at("chanA").handle(contracts.Event{T: "human"})
	set.at("chanB").handle(contracts.Event{T: "human"})
	set.at("chanA").handle(contracts.Event{T: "reply", Text: "answer A", Done: true})

	if got := f.postsTo("chanB"); len(got) != 0 {
		t.Fatalf("conversation B received %v; A's reply leaked across channels", got)
	}
	if got := f.postsTo("chanA"); len(got) != 1 || got[0] != "answer A" {
		t.Fatalf("conversation A posts = %v, want [answer A]", got)
	}
	if set.at("chanA") != set.at("chanA") {
		t.Fatal("at() returned a fresh sink for a known conversation — per-turn state would be lost")
	}
}

func TestGatewayEmitToRoutesByConversation(t *testing.T) {
	f := &fakeRender{}
	g := &Gateway{sinks: newSinks(context.Background(), f, staticLevels("full"), "", false)}

	g.EmitTo(contracts.Conversation{ID: "chanA"}, contracts.Event{T: "reply", Text: "hello A", Done: true})
	// An unrouted event has no conversation to render into and must be dropped
	// rather than guessing a channel.
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
	s.noteUser("c1", "u1")

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

// TestSinkAbandonedClearsAck proves an abandoned turn (no reply) clears the ⏳
// ACK and posts no final reply: the host's abstract "abandoned" signal is
// rendered by dropping the pending marker, not by a misleading ✅ summary.
func TestSinkAbandonedClearsAck(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.noteUser("c1", "u1")
	s.handle(contracts.Event{T: "human"})
	// First status flushes immediately (lastEdit zero); the second is coalesced
	// inside the throttle window and stays unflushed until abandon forces it.
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
	// The forced final flush preserves the coalesced line as the last honest state.
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

// TestSinkResetDiscardsAndContinues proves a mid-turn reset (backend
// crash+retry) discards the partial render in place — keeping the ⏳ ACK and
// the same live message — and that the retried turn keeps rendering, ending on
// the normal ✅ summary rather than a misleading ⚠️.
func TestSinkResetDiscardsAndContinues(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.noteUser("c1", "u1")
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "status", Text: "Read x"})
	s.handle(contracts.Event{T: "reset"})

	// Reset must not collapse to a summary and must not remove the ⏳ ACK.
	afterReset := f.upserts[len(f.upserts)-1]
	if strings.HasPrefix(afterReset, "✅") || strings.HasPrefix(afterReset, "⚠️") {
		t.Fatalf("reset status = %q, want in-progress (no summary)", afterReset)
	}
	if len(f.unreacted) != 0 {
		t.Fatalf("unreacted = %v, want ACK kept during retry", f.unreacted)
	}

	// The retried turn keeps rendering and completes normally.
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
