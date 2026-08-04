package discord

import (
	"fmt"
	"strings"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

// maxLines caps the live progress body so it stays under Discord's 2000-char
// message limit; older lines are elided with a leading "…".
const maxLines = 15

// progressInterval throttles live edits so a tool-heavy turn does not hammer
// Discord's per-channel edit rate limit. Events are coalesced between edits.
const progressInterval = 1500 * time.Millisecond

// Render levels, quietest first. silent posts nothing at all: no live message,
// no summary, only the reply the turn ends on. It is the default because the
// bot is pinged in rooms other people read, and a wall of tool names is noise
// there — the levels above it are opt-in for when the operator is watching a
// turn and wants to see it work.
const (
	levelSilent  = "silent"
	levelQuiet   = "quiet"
	levelActions = "actions"
	levelFull    = "full"

	defaultLevel = levelSilent
)

// rendersProgress reports whether a level posts anything before the reply.
func rendersProgress(level string) bool { return level != levelSilent }

// progressView accumulates one turn's activity and pushes it to a single
// live-updating Discord message, then collapses it to a one-line summary. post
// creates (empty id) or edits (non-empty id) the message and returns its id.
// It is never built at the silent level — see sink.handle.
type progressView struct {
	post      func(msgID, content string) (string, error)
	level     string // levelQuiet | levelActions | levelFull
	start     time.Time
	now       func() time.Time
	lines     []string
	elided    bool // older lines were dropped past maxLines (render shows a leading "…")
	lastLine  string
	lastCount int
	counts    map[string]int
	order     []string
	cost      float64
	actions   int
	msgID     string
	lastEdit  time.Time
	dirty     bool
}

func newProgressView(post func(string, string) (string, error), level string, start time.Time) *progressView {
	return &progressView{post: post, level: level, start: start, now: time.Now, counts: map[string]int{}}
}

func (p *progressView) add(ev contracts.BackendEvent) {
	switch ev.Kind {
	case "result":
		p.cost = ev.Cost
		return
	case "text":
		if p.level != levelFull {
			return
		}
		p.push("💭 " + clip(flatten(ev.Detail), 120))
	case "tool":
		if _, seen := p.counts[ev.Tool]; !seen {
			p.order = append(p.order, ev.Tool)
		}
		p.counts[ev.Tool]++
		p.actions++
		line := emojiFor(ev.Tool) + " " + ev.Tool
		// A tool detail is a file path, a shell command or a search pattern: it
		// exposes the machine's layout to everyone who can read the channel.
		// "quiet" keeps the pulse of the turn and drops what it was aimed at.
		if d := clip(flatten(ev.Detail), 120); d != "" && p.level != levelQuiet {
			line += " · " + d
		}
		p.push(line)
	default:
		return
	}
	// Only the last maxLines are ever rendered; drop the tail past that so the
	// slice does not retain every line for the whole turn.
	if len(p.lines) > maxLines {
		p.lines = append(p.lines[:0], p.lines[len(p.lines)-maxLines:]...)
		p.elided = true
	}
	p.dirty = true
	p.flush(false)
}

// push appends a rendered line, collapsing an immediate repeat into "×N".
// Stripped of their details ("quiet"), a burst of the same tool is a run of
// identical lines that would push everything else out of the 15-line window.
func (p *progressView) push(line string) {
	if p.lastCount > 0 && line == p.lastLine {
		p.lastCount++
		p.lines[len(p.lines)-1] = fmt.Sprintf("%s ×%d", line, p.lastCount)
		return
	}
	p.lastLine, p.lastCount = line, 1
	p.lines = append(p.lines, line)
}

func (p *progressView) flush(force bool) {
	if !p.dirty || p.post == nil {
		return
	}
	if !force && !p.lastEdit.IsZero() && p.now().Sub(p.lastEdit) < progressInterval {
		return
	}
	id, err := p.post(p.msgID, p.render())
	if err != nil {
		return
	}
	p.msgID = id
	p.lastEdit = p.now()
	p.dirty = false
}

func (p *progressView) finish() {
	if len(p.lines) == 0 {
		if p.msgID != "" && p.post != nil {
			_, _ = p.post(p.msgID, p.summary())
		}
		return
	}
	if p.post != nil {
		_, _ = p.post(p.msgID, p.summary())
	}
}

// reset discards the current turn's accumulated activity after a backend
// crash+retry, keeping the same live message and elapsed clock so the retried
// turn keeps rendering in place. The turn is not over, so it must NOT collapse
// to a summary (that is what the old finish-on-reset path got wrong).
func (p *progressView) reset() {
	p.lines = nil
	p.elided = false
	p.lastLine, p.lastCount = "", 0
	p.counts = map[string]int{}
	p.order = nil
	p.cost = 0
	p.actions = 0
	if p.msgID == "" {
		return // nothing shown yet; let the retried turn create the message
	}
	p.dirty = true
	p.flush(true)
}

func (p *progressView) render() string {
	var b strings.Builder
	b.WriteString("⏳ en cours…\n")
	if p.elided {
		b.WriteString("…\n")
	}
	b.WriteString(strings.Join(p.lines, "\n"))
	return b.String()
}

func (p *progressView) summary() string {
	icon := "✅"
	parts := make([]string, 0, len(p.order))
	for _, name := range p.order {
		if n := p.counts[name]; n > 1 {
			parts = append(parts, fmt.Sprintf("%s×%d", name, n))
		} else {
			parts = append(parts, name)
		}
	}
	var s string
	if p.actions == 0 {
		s = icon + " terminé"
	} else {
		s = fmt.Sprintf("%s %d action%s", icon, p.actions, plural(p.actions))
		if len(parts) > 0 {
			s += " (" + strings.Join(parts, ", ") + ")"
		}
	}
	s += fmt.Sprintf(" · %ds", int(p.now().Sub(p.start).Round(time.Second).Seconds()))
	if p.cost > 0 {
		s += " · " + formatCost(p.cost)
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func formatCost(c float64) string {
	if c < 0.01 {
		return fmt.Sprintf("$%.4f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

func emojiFor(tool string) string {
	switch tool {
	case "Read":
		return "📖"
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return "✏️"
	case "Grep", "Glob":
		return "🔎"
	case "Task", "Agent":
		return "🤖"
	case "WebFetch", "WebSearch":
		return "🌐"
	case "TodoWrite":
		return "📝"
	default:
		return "🔧"
	}
}

func flatten(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
