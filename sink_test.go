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

// TestSinkAbandonedClearsAck proves an abandoned turn (no reply) clears the ⏳
// ACK and posts no final reply: the host's abstract "abandoned" signal is
// rendered by dropping the pending marker, not by a misleading ✅ summary.
func TestSinkAbandonedClearsAck(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.noteUser("u1")
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
	s.noteUser("u1")
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
