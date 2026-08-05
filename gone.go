package discord

import (
	"context"
	"fmt"
	"os"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

// channelDelete is the subset of a CHANNEL_DELETE / THREAD_DELETE dispatch the
// gateway reads. Only the id matters: whatever the conversation was, it is gone.
type channelDelete struct {
	ID string `json:"id"`
}

// onChannelGone tears down the session a deleted conversation was driving.
//
// Nothing else does. A session is a supervised bridge that is restarted forever
// and only ever stopped by an explicit close, so a conversation that disappears
// out from under one leaves a live agent talking into a 404: it keeps its
// worktree, keeps burning tokens on a turn nobody will read, and every reply it
// posts fails. The operator sees a bot that stopped answering; the machine sees
// a process that never ends. Deleting the room is the clearest possible way of
// saying the job is over, so it is what ends the job.
//
// Every channel deleted anywhere the bot is a member arrives here, so the
// no-session case has to cost nothing: it returns before writing the bind store
// to disk. The binding still goes before the close, and whether the close
// succeeds or not — a stale binding is the worse outcome, silently routing a
// reused id's messages into a session that does not serve it.
func (r *router) onChannelGone(ctx context.Context, id string) {
	// Deleting a channel takes its threads with it, but Discord announces only the
	// channel. A job the gateway moved into a private thread would otherwise be
	// the one case this whole path misses — and it is the common case, since that
	// is where jobs are asked to run.
	for _, child := range r.binds.Children(id) {
		r.endConversation(ctx, child)
		// endConversation only writes the store for a thread that had a session;
		// this drops the parent link of one that no longer did, which would
		// otherwise outlive both the thread and its channel.
		if err := r.binds.Forget(child); err != nil {
			fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
		}
	}
	r.endConversation(ctx, id)
}

func (r *router) endConversation(ctx context.Context, id string) {
	session := r.binds.Session(id)
	r.forget(id)
	r.sinks.drop(id)
	if session == "" {
		return
	}
	if err := r.binds.Forget(id); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
	}
	ctrl := r.ctrl()
	if ctrl == nil {
		return
	}
	// Stop the in-flight turn before closing. Its answer has nowhere to go, and a
	// close that races a running backend is a close fighting a process that is
	// still writing to the worktree it is trying to remove.
	ctrl.Interrupt(session)
	closeGone(ctx, ctrl, session, id)
}

// closeGone closes the session, preferring the non-destructive close and falling
// back to the forcing one.
//
// The plain close refuses when the worktree has uncommitted changes — the right
// answer when a human asked for it, because they can be told to commit first.
// Here there is nobody to tell: the conversation that would carry the question
// is the one that was just deleted. Leaving the session up instead would restore
// exactly the failure this whole path exists to remove, so the force is taken,
// loudly. It discards uncommitted changes only: every commit the agent made is
// on branch session/<name>, which close keeps either way.
func closeGone(ctx context.Context, ctrl contracts.SessionControl, session, conv string) {
	if _, err := ctrl.Close(ctx, session, false); err == nil {
		fmt.Fprintf(os.Stderr, "discord gateway: conversation %s deleted, closed session %s\n", conv, session)
		return
	}
	if _, err := ctrl.Close(ctx, session, true); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: conversation %s deleted, but closing session %s failed: %v\n", conv, session, err)
		return
	}
	fmt.Fprintf(os.Stderr, "discord gateway: conversation %s deleted, force-closed session %s — uncommitted worktree changes were discarded, branch session/%s is kept\n", conv, session, session)
}

// forget drops the router's memory of a conversation: a buffered ping still
// waiting on a repo answer, and the job that answer would have created. Both are
// keyed by conversation, and neither can ever be replayed once the room is gone.
func (r *router) forget(conv string) {
	r.mu.Lock()
	delete(r.pending, conv)
	delete(r.jobs, conv)
	r.mu.Unlock()
}
