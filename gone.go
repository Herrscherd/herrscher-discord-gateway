package discord

import (
	"context"
	"fmt"
	"os"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type channelDelete struct {
	ID string `json:"id"`
}

func (r *router) onChannelGone(ctx context.Context, id string) {
	for _, child := range r.binds.Children(id) {
		r.endConversation(ctx, child)
		if err := r.binds.Forget(child); err != nil {
			fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
		}
	}
	r.endConversation(ctx, id)
}

func (r *router) endConversation(ctx context.Context, id string) {
	sessions := r.binds.SessionsIn(id)
	r.forget(id)
	r.sinks.drop(id)
	if len(sessions) == 0 {
		return
	}
	if err := r.binds.Forget(id); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
	}
	ctrl := r.ctrl()
	if ctrl == nil {
		return
	}
	for _, session := range sessions {
		ctrl.Interrupt(session)
		closeGone(ctx, ctrl, session, id)
	}
}

func closeGone(ctx context.Context, ctrl contracts.SessionControl, session, conv string) {
	if _, err := ctrl.Close(ctx, session, false); err == nil {
		fmt.Fprintf(os.Stderr, "discord gateway: conversation %s deleted, closed session %s\n", conv, session)
		return
	}
	if _, err := ctrl.Close(ctx, session, true); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: conversation %s deleted, but closing session %s failed: %v\n", conv, session, err)
		return
	}
	fmt.Fprintf(os.Stderr, "discord gateway: conversation %s deleted, force-closed session %s, uncommitted worktree changes were discarded, branch session/%s is kept\n", conv, session, session)
}

func (r *router) forget(conv string) {
	r.mu.Lock()
	delete(r.pending, conv)
	delete(r.jobs, conv)
	r.mu.Unlock()
}
