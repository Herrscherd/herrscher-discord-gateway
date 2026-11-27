package discord

import (
	"context"
	"strings"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

func (r *router) bindKnownRepo(ctx context.Context, ctrl contracts.SessionControl, task job, msg messageCreate, repos []contracts.RepoRef) bool {
	if value := r.binds.Repo(task.parent); value != "" {
		r.bindOrFail(ctx, ctrl, task, value, msg)
		return true
	}
	if repo, ok := matchRepo(msg.Content, repos); ok {
		r.bindOrFail(ctx, ctrl, task, repoValue(repo), msg)
		return true
	}
	last := r.binds.LastRepo()
	if last == "" || !knownRepo(last, repos) {
		return false
	}
	if failure := r.bind(ctx, ctrl, task, last, msg, true); failure != "" {
		r.sinks.at(task.conv).fail(ctx, failure)
		return true
	}
	r.post(ctx, task.conv, r.textsIn(task.conv).say("discord.repo.reused", map[string]any{"Repo": neutralise(repoLabel(last))}))
	return true
}

func (r *router) bindOrFail(ctx context.Context, ctrl contracts.SessionControl, task job, value string, m messageCreate) {
	if failure := r.bind(ctx, ctrl, task, value, m, true); failure != "" {
		r.sinks.at(task.conv).fail(ctx, failure)
	}
}

func (r *router) askRepo(ctx context.Context, task job, msg messageCreate, repos []contracts.RepoRef) {
	conv := task.conv
	r.mu.Lock()
	r.pending[conv] = msg
	r.jobs[conv] = task
	r.mu.Unlock()

	prompt := r.msgs.say("discord.repo.ask", nil)
	if len(repos) > selectMenuMax {
		prompt = r.msgs.say("discord.repo.ask_capped", map[string]any{"Question": prompt, countArg: len(repos), "Shown": selectMenuMax})
	}
	replyTo := msg.ID
	if conv != msg.ChannelID {
		replyTo = ""
	}
	if _, err := r.c.SendSelectMenu(ctx, conv, replyTo, prompt, BindCustomID(conv), repoOptions(repos)); err != nil {
		r.log.Warn("repo menu failed", "err", err)
		r.mu.Lock()
		delete(r.pending, conv)
		delete(r.jobs, conv)
		r.mu.Unlock()
		r.sinks.at(conv).fail(ctx, r.msgs.say("discord.repo.menu_failed", nil))
	}
}

func repoOptions(repos []contracts.RepoRef) []dctl.SelectOption {
	opts := make([]dctl.SelectOption, 0, min(len(repos), selectMenuMax))
	for _, repo := range repos {
		if len(opts) == selectMenuMax {
			break
		}
		opts = append(opts, dctl.SelectOption{Label: repo.Name, Value: repoValue(repo), Description: repo.Description})
	}
	return opts
}

func repoOf(ctrl contracts.SessionControl, session string) (string, bool) {
	items := ctrl.Sessions()
	for i := range items {
		item := items[i]
		if item.Name != session {
			continue
		}
		if item.Project == "" {
			return "", false
		}
		return "local:" + item.Project, true
	}
	return "", false
}

func knownRepo(value string, repos []contracts.RepoRef) bool {
	for _, repo := range repos {
		if repoValue(repo) == value {
			return true
		}
	}
	return false
}

func repoLabel(value string) string {
	if _, name, found := strings.Cut(value, ":"); found {
		return name
	}
	return value
}

func repoValue(repo contracts.RepoRef) string {
	if repo.Local {
		return "local:" + repo.Name
	}
	return "remote:" + repo.Name
}
