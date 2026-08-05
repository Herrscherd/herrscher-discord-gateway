package discord

import (
	"context"
	"errors"
	"testing"
)

// A deleted conversation takes its session with it. Nothing else would: a
// session's bridge is restarted forever and only an explicit close stops it, so
// without this the agent keeps running against a room that no longer exists.
func TestDeletedConversationClosesItsSession(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	ctrl.live["ch-c1"] = true

	r.onChannelGone(context.Background(), "c1")

	if len(ctrl.closed) != 1 || ctrl.closed[0] != (closeCall{name: "ch-c1"}) {
		t.Fatalf("closed = %+v, want one plain close of ch-c1", ctrl.closed)
	}
	if len(ctrl.interrupted) != 1 || ctrl.interrupted[0] != "ch-c1" {
		t.Fatalf("interrupted = %v, want the in-flight turn stopped first", ctrl.interrupted)
	}
	if s := r.binds.Session("c1"); s != "" {
		t.Fatalf("c1 still bound to %q — a reused id would route into a dead session", s)
	}
}

// A worktree the agent left uncommitted makes the plain close fail. There is
// nobody left to ask to commit — the conversation that would carry the question
// is the one that was deleted — so the force is taken rather than leaving the
// session running, which is the failure this path exists to remove.
func TestDeletedConversationForcesWhenCloseRefuses(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	ctrl.live["ch-c1"] = true
	ctrl.closeErr = errors.New("worktree has uncommitted changes")

	r.onChannelGone(context.Background(), "c1")

	want := []closeCall{{name: "ch-c1"}, {name: "ch-c1", force: true}}
	if len(ctrl.closed) != 2 || ctrl.closed[0] != want[0] || ctrl.closed[1] != want[1] {
		t.Fatalf("closed = %+v, want %+v", ctrl.closed, want)
	}
}

// A conversation with no session still gets its router state dropped, and does
// not reach the core: there is nothing there to close.
func TestDeletedUnboundConversationClosesNothing(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	// A ping was waiting on the repo menu when the room went away. Replaying it
	// later is impossible, and keeping it leaks one messageCreate per deletion.
	r.mu.Lock()
	r.pending["c1"] = ownerPing("fix the login bug")
	r.jobs["c1"] = job{conv: "c1", parent: "c1"}
	r.mu.Unlock()

	r.onChannelGone(context.Background(), "c1")

	if len(ctrl.closed) != 0 {
		t.Fatalf("closed = %+v, want nothing closed for an unbound conversation", ctrl.closed)
	}
	if len(c.menus) != 0 {
		t.Fatalf("menus = %+v, want no Discord call into a deleted room", c.menus)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) != 0 || len(r.jobs) != 0 {
		t.Fatalf("pending=%v jobs=%v, want both dropped", r.pending, r.jobs)
	}
}
