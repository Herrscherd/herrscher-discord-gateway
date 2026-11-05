package discord

import (
	"context"
	"errors"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func tidySink(f *fakeRender, ch string) *sink {
	return newSink(testRun(), f, ch, staticLevel(levelSilent), "42", true, testMessages(), testLog())
}

func TestTidyDeletesThePingOnceAnswered(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "c1")
	s.ack(context.Background(), "c1", "m1", "42")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 1 || f.deleted[0] != (msgRef{ch: "c1", id: "m1"}) {
		t.Fatalf("deleted = %+v, want the ping m1 in c1", f.deleted)
	}
	if len(f.unreacted) != 0 {
		t.Fatalf("unreacted = %v, want nothing, the reaction went with the message", f.unreacted)
	}
	if len(f.posts) != 1 {
		t.Fatalf("posts = %v, want the answer posted before the ping was deleted", f.posts)
	}
}

func TestTidyOffKeepsThePing(t *testing.T) {
	f := &fakeRender{}
	s := newSink(testRun(), f, "c1", staticLevel(levelSilent), "42", false, testMessages(), testLog())
	s.ack(context.Background(), "c1", "m1", "42")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %+v, want nothing deleted with tidy off", f.deleted)
	}
	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared the ordinary way", f.unreacted)
	}
}

func TestTidyKeepsThePingOnAbandonedTurn(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "c1")
	s.ack(context.Background(), "c1", "m1", "42")
	s.handle(contracts.Event{T: "abandoned"})

	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %+v, want the ping kept when the turn produced nothing", f.deleted)
	}
	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared so the turn stops reading as pending", f.unreacted)
	}
}

func TestTidyNeverDeletesAPingOutsideItsOwnConversation(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "thread1")
	s.ack(context.Background(), "c1", "m1", "42")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %+v, want the ping left alone, the thread hangs off it", f.deleted)
	}
	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared instead", f.unreacted)
	}
}

func TestTidyFallsBackToClearingTheAckWhenDeleteFails(t *testing.T) {
	f := &fakeRender{deleteErr: errors.New("missing permissions")}
	s := tidySink(f, "c1")
	s.ack(context.Background(), "c1", "m1", "42")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared after the delete was refused", f.unreacted)
	}
}

func TestTidyKeepsAPingThatArrivedMidTurn(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "c1")
	s.ack(context.Background(), "c1", "m1", "42")
	s.noteUser(context.Background(), "c1", "m2", "42")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 1 || f.deleted[0].id != "m1" {
		t.Fatalf("deleted = %+v, want only the ping this turn answered", f.deleted)
	}
	if s.lastUser != (msgRef{ch: "c1", id: "m2", author: "42"}) {
		t.Fatalf("lastUser = %+v, want the mid-turn ping still queued for its ack", s.lastUser)
	}
}

func TestTidyNeverDeletesABystandersMessage(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "c1")
	s.ack(context.Background(), "c1", "m1", "99")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %+v, want a message written by somebody else left alone", f.deleted)
	}
	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared the ordinary way", f.unreacted)
	}
}
