package discord

import (
	"context"
	"errors"
	"testing"
)

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
		t.Fatalf("c1 still bound to %q, a reused id would route into a dead session", s)
	}
}

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

func TestDeletedUnboundConversationClosesNothing(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
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

func TestDeletedChannelClosesTheSessionsOfItsThreads(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	if err := r.binds.BindThread("t1", "t1", "c1", "ch-t1"); err != nil {
		t.Fatal(err)
	}
	ctrl.live["ch-t1"] = true

	r.onChannelGone(context.Background(), "c1")

	if len(ctrl.closed) != 1 || ctrl.closed[0] != (closeCall{name: "ch-t1"}) {
		t.Fatalf("closed = %+v, want the thread's session closed with its channel", ctrl.closed)
	}
	if s := r.binds.Session("t1"); s != "" {
		t.Fatalf("t1 still bound to %q", s)
	}
	if len(r.binds.Children("c1")) != 0 {
		t.Fatal("the parent link outlived both the thread and its channel")
	}
}
