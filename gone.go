package discord

import (
	"context"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type channelDelete struct {
	ID string `json:"id"`
}

func (r *router) onChannelGone(ctx context.Context, channelID string) {
	for _, child := range r.binds.Children(channelID) {
		r.endConversation(ctx, child)
		if err := r.binds.Forget(child); err != nil {
			r.log.Warn("bind store save failed", "err", err)
		}
	}
	r.endConversation(ctx, channelID)
}

func (r *router) endConversation(ctx context.Context, conversationID string) {
	sessions := r.binds.SessionsIn(conversationID)
	r.forget(conversationID)
	r.sinks.drop(conversationID)
	if len(sessions) == 0 {
		return
	}
	if err := r.binds.Forget(conversationID); err != nil {
		r.log.Warn("bind store save failed", "err", err)
	}
	ctrl := r.ctrl()
	if ctrl == nil {
		return
	}
	for _, session := range sessions {
		ctrl.Interrupt(session)
		r.closeGone(ctx, ctrl, session, conversationID)
	}
}

func (r *router) closeGone(ctx context.Context, ctrl contracts.SessionControl, session, conv string) {
	if _, err := ctrl.Close(ctx, session, false); err == nil {
		r.log.Info("conversation deleted, session closed", "conv", conv, "session", session)
		return
	}
	if _, err := ctrl.Close(ctx, session, true); err != nil {
		r.log.Warn("conversation deleted, closing session failed", "conv", conv, "session", session, "err", err)
		return
	}
	r.log.Warn("conversation deleted, session force-closed, uncommitted worktree changes discarded", "conv", conv, "session", session, "branch", "session/"+session)
}

func (r *router) forget(conv string) {
	r.mu.Lock()
	delete(r.pending, conv)
	delete(r.jobs, conv)
	r.mu.Unlock()
}
