package discord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	maxFileBytes    int64 = 8 << 20
	filesPerMessage       = 10
	maxFilesPerSend       = 50
	namesArg              = "Names"
)

var errFileRefused = errors.New("file not sent")

type openFile struct {
	file dctl.File
	src  *os.File
}

// SendFiles posts text in reply to replyTo in conv, with files attached, at most ten
// files to a message. A file that is missing, unreadable, reached through a symlink,
// not a regular file, larger than the size limit, badly named, or past the fiftieth,
// is not sent: its name is listed at the end of the text. It returns the id of the
// first message sent, and an error that wraps the failure. It satisfies
// contracts.FileSender.
func (g *Gateway) SendFiles(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string, files []contracts.OutboundFile) (contracts.MessageID, error) {
	sorted := g.openFiles(files)
	sent, err := g.sendOpened(ctx, conv, replyTo, g.withRefusals(text, sorted.refused), sorted.ready)
	return sent, errors.Join(err, closeFiles(sorted.ready))
}

func (g *Gateway) sendOpened(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string, ready []openFile) (contracts.MessageID, error) {
	parts := chunkText(text, gatewayMaxLen)
	if len(ready) == 0 {
		return g.sendParts(ctx, conv, replyTo, parts)
	}
	lead, last := parts, ""
	if len(parts) > 0 {
		lead, last = parts[:len(parts)-1], parts[len(parts)-1]
	}
	first, err := g.sendParts(ctx, conv, replyTo, lead)
	if err != nil {
		return first, err
	}
	if len(lead) > 0 {
		replyTo = ""
	}
	batched, err := g.sendBatches(ctx, conv, replyTo, last, ready)
	return keepFirst(first, batched), err
}

type sortedFiles struct {
	ready   []openFile
	refused []string
}

func (g *Gateway) openFiles(files []contracts.OutboundFile) sortedFiles {
	sorted := sortedFiles{ready: make([]openFile, 0, len(files))}
	for index, out := range files {
		if index >= maxFilesPerSend {
			sorted.refused = append(sorted.refused, out.Name)
			continue
		}
		opened, err := openOutbound(out, g.fileLimit)
		if err != nil {
			g.log.Warn("outbound file not sent", "file", out.Name, "err", err)
			sorted.refused = append(sorted.refused, out.Name)
			continue
		}
		sorted.ready = append(sorted.ready, opened)
	}
	return sorted
}

func openOutbound(out contracts.OutboundFile, limit int64) (openFile, error) {
	if !utf8.ValidString(out.Name) || strings.ContainsFunc(out.Name, unicode.IsControl) {
		return openFile{}, fmt.Errorf("%w: %q is not a valid file name", errFileRefused, out.Name)
	}
	resolved, err := filepath.EvalSymlinks(out.Path)
	if err != nil {
		return openFile{}, fmt.Errorf("%w: %q: %w", errFileRefused, out.Name, err)
	}
	if resolved != filepath.Clean(out.Path) {
		return openFile{}, fmt.Errorf("%w: %q is reached through a symlink", errFileRefused, out.Name)
	}
	info, err := os.Lstat(out.Path)
	if err != nil {
		return openFile{}, fmt.Errorf("%w: %q: %w", errFileRefused, out.Name, err)
	}
	if !info.Mode().IsRegular() {
		return openFile{}, fmt.Errorf("%w: %q is not a regular file", errFileRefused, out.Name)
	}
	src, err := os.Open(out.Path)
	if err != nil {
		return openFile{}, fmt.Errorf("%w: %q: %w", errFileRefused, out.Name, err)
	}
	if err = checkOpened(src, info, out.Name, limit); err != nil {
		return openFile{}, errors.Join(err, src.Close())
	}
	content := io.LimitReader(src, limit)
	return openFile{file: dctl.File{Name: out.Name, Content: content, ContentType: out.ContentType}, src: src}, nil
}

func checkOpened(src *os.File, checked os.FileInfo, name string, limit int64) error {
	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("%w: %q: %w", errFileRefused, name, err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(checked, info) {
		return fmt.Errorf("%w: %q changed while it was opened", errFileRefused, name)
	}
	if info.Size() > limit {
		return fmt.Errorf("%w: %q is %d bytes, the limit is %d", errFileRefused, name, info.Size(), limit)
	}
	return nil
}

func closeFiles(opened []openFile) error {
	var err error
	for _, f := range opened {
		err = errors.Join(err, f.src.Close())
	}
	return err
}

func (g *Gateway) withRefusals(text string, refused []string) string {
	if len(refused) == 0 {
		return text
	}
	notice := g.msgs.say("discord.files.refused", map[string]any{namesArg: strings.Join(refused, ", ")})
	if text == "" {
		return notice
	}
	return text + "\n" + notice
}

func (g *Gateway) sendParts(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, parts []string) (contracts.MessageID, error) {
	var first contracts.MessageID
	for _, part := range parts {
		sentID, err := g.sendText(ctx, conv, replyTo, part)
		if err != nil {
			return first, err
		}
		first = keepFirst(first, sentID)
		replyTo = ""
	}
	return first, nil
}

func (g *Gateway) sendText(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string) (contracts.MessageID, error) {
	if replyTo == "" {
		return g.Post(ctx, conv, text)
	}
	return g.Reply(ctx, conv, replyTo, text)
}

func (g *Gateway) sendBatches(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string, files []openFile) (contracts.MessageID, error) {
	var first contracts.MessageID
	for start := 0; start < len(files); start += filesPerMessage {
		content := ""
		if start == 0 {
			content = text
		}
		end := min(start+filesPerMessage, len(files))
		sentID, err := g.sendFileBatch(ctx, conv, replyTo, content, files[start:end])
		if err != nil {
			return first, err
		}
		first = keepFirst(first, sentID)
		replyTo = ""
	}
	return first, nil
}

func (g *Gateway) sendFileBatch(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string, batch []openFile) (contracts.MessageID, error) {
	files := make([]dctl.File, 0, len(batch))
	for _, opened := range batch {
		files = append(files, opened.file)
	}
	var sent *dctl.Message
	var err error
	if replyTo == "" {
		sent, err = g.c.SendFiles(ctx, conv.ID, text, files)
	} else {
		sent, err = g.c.ReplyFiles(ctx, conv.ID, string(replyTo), text, files)
	}
	if err != nil {
		return msgID(sent), fmt.Errorf("discord send files: %w", err)
	}
	return msgID(sent), nil
}

func keepFirst(first, next contracts.MessageID) contracts.MessageID {
	if first != "" {
		return first
	}
	return next
}
