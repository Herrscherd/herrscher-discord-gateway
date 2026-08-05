package discord

import (
	"context"
	"errors"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

// tidySink is a sink with the tidy option on, rendering into ch.
func tidySink(f *fakeRender, ch string) *sink {
	return newSink(context.Background(), f, ch, staticLevel(levelSilent), "42", true)
}

// The answer is posted, then the ping that asked for it goes: the channel keeps
// the work, not the requests for it. The ⏳ is not removed separately — it went
// with the message.
func TestTidyDeletesThePingOnceAnswered(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "c1")
	s.ack("c1", "m1", "42")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 1 || f.deleted[0] != (msgRef{ch: "c1", id: "m1"}) {
		t.Fatalf("deleted = %+v, want the ping m1 in c1", f.deleted)
	}
	if len(f.unreacted) != 0 {
		t.Fatalf("unreacted = %v, want nothing — the reaction went with the message", f.unreacted)
	}
	if len(f.posts) != 1 {
		t.Fatalf("posts = %v, want the answer posted before the ping was deleted", f.posts)
	}
}

// Off by default. Deleting somebody's message is not undoable, so it happens
// only when it was asked for.
func TestTidyOffKeepsThePing(t *testing.T) {
	f := &fakeRender{}
	s := newSink(context.Background(), f, "c1", staticLevel(levelSilent), "42", false)
	s.ack("c1", "m1", "42")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %+v, want nothing deleted with tidy off", f.deleted)
	}
	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared the ordinary way", f.unreacted)
	}
}

// A turn that ended with no answer keeps its ping. It is the only remaining
// record of what was wanted, and deleting it would leave the operator with a
// conversation that shows neither the request nor a result.
func TestTidyKeepsThePingOnAbandonedTurn(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "c1")
	s.ack("c1", "m1", "42")
	s.handle(contracts.Event{T: "abandoned"})

	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %+v, want the ping kept when the turn produced nothing", f.deleted)
	}
	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared so the turn stops reading as pending", f.unreacted)
	}
}

// A ping written somewhere other than where the answer landed is never touched.
// In support mode the thread is started ON the ping, and Discord deletes a
// thread along with the message it hangs off — tidying there would delete the
// answer and the whole conversation with it.
func TestTidyNeverDeletesAPingOutsideItsOwnConversation(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "thread1")
	s.ack("c1", "m1", "42") // the ping stayed in the channel; the job moved to the thread
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %+v, want the ping left alone — the thread hangs off it", f.deleted)
	}
	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared instead", f.unreacted)
	}
}

// A delete the bot is not allowed to make must not leave the turn looking
// pending: the ⏳ is cleared the ordinary way instead.
func TestTidyFallsBackToClearingTheAckWhenDeleteFails(t *testing.T) {
	f := &fakeRender{deleteErr: errors.New("missing permissions")}
	s := tidySink(f, "c1")
	s.ack("c1", "m1", "42")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared after the delete was refused", f.unreacted)
	}
}

// A ping that arrived mid-turn is waiting for its own ⏳; deleting this turn's
// trigger must not forget it, or the next turn renders unmarked.
func TestTidyKeepsAPingThatArrivedMidTurn(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "c1")
	s.ack("c1", "m1", "42")
	s.noteUser("c1", "m2", "42")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 1 || f.deleted[0].id != "m1" {
		t.Fatalf("deleted = %+v, want only the ping this turn answered", f.deleted)
	}
	if s.lastUser != (msgRef{ch: "c1", id: "m2", author: "42"}) {
		t.Fatalf("lastUser = %+v, want the mid-turn ping still queued for its ack", s.lastUser)
	}
}

// The polling reader records the last non-bot message whoever wrote it, so the
// ref a turn acks is not always the operator's. Tidy deletes the operator's own
// ping and nothing else: removing a bystander's message is not tidying, it is
// moderating.
func TestTidyNeverDeletesABystandersMessage(t *testing.T) {
	f := &fakeRender{}
	s := tidySink(f, "c1")
	s.ack("c1", "m1", "99")
	s.handle(contracts.Event{T: "reply", Done: true, Text: "voilà"})

	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %+v, want a message written by somebody else left alone", f.deleted)
	}
	if len(f.unreacted) != 1 {
		t.Fatalf("unreacted = %v, want the ⏳ cleared the ordinary way", f.unreacted)
	}
}
