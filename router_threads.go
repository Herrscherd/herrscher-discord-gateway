package discord

import (
	"context"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func (r *router) place(ctx context.Context, msg messageCreate) job {
	task := r.conversation(ctx, msg)
	chatType := chatChannel
	switch {
	case msg.direct():
		chatType = chatDM
	case task.thread:
		chatType = chatThread
	}
	task.slot = r.cfg.scope.slot(chatType, task.conv, msg.Author.ID)
	task.shared = r.cfg.scope.multiUser(chatType)
	return task
}

func (r *router) conversation(ctx context.Context, msg messageCreate) job {
	here := job{conv: msg.ChannelID, parent: msg.ChannelID}
	if r.binds.IsThread(msg.ChannelID) {
		here.thread = true
		return here
	}
	if r.binds.Mode(msg.ChannelID) == supportMode {
		threadID, err := r.c.StartThread(ctx, msg.ChannelID, msg.ID, threadName(msg.Content))
		if err == nil && threadID != "" {
			r.adoptThread(ctx, msg.ChannelID, threadID)
			return job{conv: threadID, thread: true, parent: msg.ChannelID}
		}
		r.log.Warn("support thread failed", "channel", msg.ChannelID, "err", err)
		r.post(ctx, msg.ChannelID, r.msgs.say("thread.public_denied", nil))
		return here
	}
	if !wantsThread(msg.Content) {
		return here
	}
	threadID, err := r.c.CreatePrivateThread(ctx, msg.ChannelID, threadName(msg.Content))
	if err == nil && threadID != "" {
		if err = r.c.AddThreadMember(ctx, threadID, msg.Author.ID); err == nil {
			r.adoptThread(ctx, msg.ChannelID, threadID)
			r.post(ctx, msg.ChannelID, r.msgs.say("thread.private_opened", map[string]any{"Channel": "<#" + threadID + ">"}))
			return job{conv: threadID, thread: true, parent: msg.ChannelID}
		}
		r.log.Warn("private thread left without its member, deleting it", "channel", msg.ChannelID, "thread", threadID, "err", err)
		r.deleteOrphanThread(ctx, msg.ChannelID, threadID)
	}
	r.log.Warn("private thread failed", "channel", msg.ChannelID, "err", err)
	r.post(ctx, msg.ChannelID, r.msgs.say("thread.private_denied", nil))
	return here
}

func (r *router) deleteOrphanThread(ctx context.Context, channel, thread string) {
	if err := r.c.DeleteChannel(ctx, thread); err != nil {
		r.log.Warn("orphan private thread could not be deleted, delete it by hand", "channel", channel, "thread", thread, "err", err)
	}
}

func (r *router) adoptThread(ctx context.Context, channel, thread string) {
	if err := r.binds.MarkThread(thread, channel); err != nil {
		r.noteSaveFailure(ctx, channel, err)
	}
	level := r.binds.Level(channel)
	if level == "" {
		return
	}
	if err := r.binds.SetLevel(thread, level); err != nil {
		r.noteSaveFailure(ctx, channel, err)
	}
}

func (r *router) fork(ctx context.Context, ctrl contracts.SessionControl, session string, msg messageCreate) bool {
	if !wantsThread(msg.Content) || r.binds.IsThread(msg.ChannelID) {
		return false
	}
	value, ok := repoOf(ctrl, session)
	if !ok {
		return false
	}
	task := r.place(ctx, msg)
	if !task.thread {
		return false
	}
	r.sinks.at(task.conv).ack(ctx, msg.ChannelID, msg.ID, msg.Author.ID)
	if failure := r.bind(ctx, ctrl, task, value, msg, true); failure != "" {
		r.sinks.at(task.conv).fail(ctx, failure)
	}
	return true
}
