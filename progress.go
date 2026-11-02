package discord

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const maxLines = 15

const previewWidth = 120

const progressInterval = 1500 * time.Millisecond

const (
	levelSilent  = "silent"
	levelQuiet   = "quiet"
	levelActions = "actions"
	levelFull    = "full"

	defaultLevel = levelSilent
)

func rendersProgress(level string) bool { return level != levelSilent }

type progressView struct {
	post      func(msgID, content string) (string, error)
	level     string
	start     time.Time
	now       func() time.Time
	log       *slog.Logger
	lines     []string
	elided    bool
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

func newProgressView(post func(string, string) (string, error), level string, start time.Time, log *slog.Logger) *progressView {
	return &progressView{post: post, level: level, start: start, now: time.Now, log: log, counts: map[string]int{}}
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
		p.push("💭 " + clip(flatten(ev.Detail), previewWidth))
	case "tool":
		if _, seen := p.counts[ev.Tool]; !seen {
			p.order = append(p.order, ev.Tool)
		}
		p.counts[ev.Tool]++
		p.actions++
		line := emojiFor(ev.Tool) + " " + ev.Tool
		if d := clip(flatten(ev.Detail), previewWidth); d != "" && p.level != levelQuiet {
			line += " · " + d
		}
		p.push(line)
	default:
		return
	}
	if len(p.lines) > maxLines {
		p.lines = append(p.lines[:0], p.lines[len(p.lines)-maxLines:]...)
		p.elided = true
	}
	p.dirty = true
	p.flush(false)
}

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
	if p.post == nil || (p.msgID == "" && len(p.lines) == 0) {
		return
	}
	if _, err := p.post(p.msgID, p.summary()); err != nil {
		p.log.Warn("progress message update failed", "err", err)
	}
}

func (p *progressView) reset() {
	p.lines = nil
	p.elided = false
	p.lastLine, p.lastCount = "", 0
	p.counts = map[string]int{}
	p.order = nil
	p.cost = 0
	p.actions = 0
	if p.msgID == "" {
		return
	}
	p.dirty = true
	p.flush(true)
}

func (p *progressView) render() string {
	lines, elided := p.lines, p.elided
	for {
		var b strings.Builder
		b.WriteString("⏳ en cours…\n")
		if elided {
			b.WriteString("…\n")
		}
		b.WriteString(strings.Join(lines, "\n"))
		out := b.String()
		if len(lines) <= 1 || utf8.RuneCountInString(out) <= gatewayMaxLen {
			return out
		}
		lines, elided = lines[1:], true
	}
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

const costPrecisionThreshold = 0.01

func formatCost(c float64) string {
	if c < costPrecisionThreshold {
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
