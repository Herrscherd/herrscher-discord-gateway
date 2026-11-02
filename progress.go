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
	return &progressView{post: post, level: level, start: start, now: time.Now, log: log, counts: make(map[string]int)}
}

func (p *progressView) add(event contracts.BackendEvent) {
	switch event.Kind {
	case "result":
		p.cost = event.Cost
		return
	case "text":
		if p.level != levelFull {
			return
		}
		p.push("💭 " + clip(flatten(event.Detail), previewWidth))
	case "tool":
		if _, seen := p.counts[event.Tool]; !seen {
			p.order = append(p.order, event.Tool)
		}
		p.counts[event.Tool]++
		p.actions++
		line := emojiFor(event.Tool) + " " + event.Tool
		if d := clip(flatten(event.Detail), previewWidth); d != "" && p.level != levelQuiet {
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
	postedID, err := p.post(p.msgID, p.render())
	if err != nil {
		return
	}
	p.msgID = postedID
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
	p.counts = make(map[string]int)
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
		out := progressFrame(lines, elided)
		if len(lines) <= 1 || utf8.RuneCountInString(out) <= gatewayMaxLen {
			return out
		}
		lines, elided = lines[1:], true
	}
}

func progressFrame(lines []string, elided bool) string {
	header := "⏳ en cours…\n"
	if elided {
		header += "…\n"
	}
	return header + strings.Join(lines, "\n")
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
	var summary string
	if p.actions == 0 {
		summary = icon + " terminé"
	} else {
		summary = fmt.Sprintf("%s %d action%s", icon, p.actions, plural(p.actions))
		if len(parts) > 0 {
			summary += " (" + strings.Join(parts, ", ") + ")"
		}
	}
	summary += fmt.Sprintf(" · %ds", int(p.now().Sub(p.start).Round(time.Second).Seconds()))
	if p.cost > 0 {
		summary += " · " + formatCost(p.cost)
	}
	return summary
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

const costPrecisionThreshold = 0.01

func formatCost(cost float64) string {
	if cost < costPrecisionThreshold {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
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

func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
