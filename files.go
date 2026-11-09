package discord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	maxFileBytes     int64 = 8 << 20
	maxBatchBytes    int64 = 10 << 20
	filesPerMessage        = 10
	maxFilesPerSend        = 50
	maxRefusedListed       = 20
	namesArg               = "Names"
)

var errFileRefused = errors.New("file not sent")

type openFile struct {
	file dctl.File
	src  *os.File
	size int64
}

// SendFiles posts text in reply to replyTo in conv, with files attached, at most ten
// files and 10 MiB to a message. A file that is missing, unreadable, reached through a
// symlink, not a regular file, larger than the size limit, badly named, or past the
// fiftieth, is not sent: its name is listed at the end of the text. A part or a batch
// that fails does not stop the next ones, and the text goes with the first batch sent,
// or alone when none is. It returns the id of the first message sent, and an
// error that joins every failure. It satisfies contracts.FileSender.
func (g *Gateway) SendFiles(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string, files []contracts.OutboundFile) (contracts.MessageID, error) {
	split := g.openFiles(files)
	sent, err := g.sendOpened(ctx, conv, replyTo, g.withRefusals(text, split.refused), split.ready)
	return sent, errors.Join(err, closeFiles(split.ready))
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
	first, leadErr := g.sendParts(ctx, conv, replyTo, lead)
	if first != "" {
		replyTo = ""
	}
	batched, err := g.sendBatches(ctx, conv, replyTo, last, ready)
	return keepFirst(first, batched), errors.Join(leadErr, err)
}

type splitFiles struct {
	ready   []openFile
	refused []string
}

func (g *Gateway) openFiles(files []contracts.OutboundFile) splitFiles {
	split := splitFiles{ready: make([]openFile, 0, len(files))}
	for index, out := range files {
		if index >= maxFilesPerSend {
			split.refused = append(split.refused, out.Name)
			continue
		}
		opened, err := openOutbound(out, g.fileLimit)
		if err != nil {
			g.log.Warn("outbound file not sent", "file", out.Name, "err", err)
			split.refused = append(split.refused, out.Name)
			continue
		}
		split.ready = append(split.ready, opened)
	}
	return split
}

func openOutbound(out contracts.OutboundFile, limit int64) (openFile, error) {
	if !validName(out.Name) {
		return openFile{}, fmt.Errorf("%w: %q is not a valid file name", errFileRefused, out.Name)
	}
	if !validContentType(out.ContentType) {
		return openFile{}, fmt.Errorf("%w: %q has an invalid content type %q", errFileRefused, out.Name, out.ContentType)
	}
	src, err := os.OpenFile(out.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return openFile{}, fmt.Errorf("%w: %q: %w", errFileRefused, out.Name, err)
	}
	size, err := checkOpened(src, out, limit)
	if err != nil {
		return openFile{}, errors.Join(err, src.Close())
	}
	content := io.LimitReader(src, limit)
	return openFile{file: dctl.File{Name: out.Name, Content: content, ContentType: out.ContentType}, src: src, size: size}, nil
}

func validName(name string) bool {
	return name != "" && utf8.ValidString(name) && !strings.ContainsFunc(name, unicode.IsControl)
}

func validContentType(contentType string) bool {
	if contentType == "" {
		return true
	}
	if strings.ContainsFunc(contentType, unicode.IsControl) {
		return false
	}
	_, _, err := mime.ParseMediaType(contentType)
	return err == nil
}

func checkOpened(src *os.File, out contracts.OutboundFile, limit int64) (int64, error) {
	opened, err := src.Stat()
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", errFileRefused, out.Name, err)
	}
	if !opened.Mode().IsRegular() {
		return 0, fmt.Errorf("%w: %q is not a regular file", errFileRefused, out.Name)
	}
	if opened.Size() > limit {
		return 0, fmt.Errorf("%w: %q is %d bytes, the limit is %d", errFileRefused, out.Name, opened.Size(), limit)
	}
	resolved, err := filepath.EvalSymlinks(out.Path)
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", errFileRefused, out.Name, err)
	}
	if resolved != filepath.Clean(out.Path) {
		return 0, fmt.Errorf("%w: %q is reached through a symlink", errFileRefused, out.Name)
	}
	onPath, err := os.Lstat(out.Path)
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", errFileRefused, out.Name, err)
	}
	if !os.SameFile(opened, onPath) {
		return 0, fmt.Errorf("%w: %q changed while it was opened", errFileRefused, out.Name)
	}
	return opened.Size(), nil
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
	notice := g.refusalNotice(refused)
	if text == "" {
		return notice
	}
	return text + "\n" + notice
}

func (g *Gateway) refusalNotice(refused []string) string {
	listed := make([]string, 0, min(len(refused), maxRefusedListed))
	for _, name := range refused[:min(len(refused), maxRefusedListed)] {
		listed = append(listed, displayName(name))
	}
	names := strings.Join(listed, ", ")
	if hidden := len(refused) - len(listed); hidden > 0 {
		return g.msgs.say("discord.files.refused_more", map[string]any{namesArg: names, countArg: hidden})
	}
	return g.msgs.say("discord.files.refused", map[string]any{namesArg: names})
}

func displayName(name string) string {
	if validName(name) {
		return neutralise(name)
	}
	return neutralise(strconv.QuoteToASCII(name))
}

func (g *Gateway) sendParts(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, parts []string) (contracts.MessageID, error) {
	var first contracts.MessageID
	var failed error
	for _, part := range parts {
		sentID, err := g.sendText(ctx, conv, replyTo, part)
		if err != nil {
			failed = errors.Join(failed, err)
			continue
		}
		first = keepFirst(first, sentID)
		replyTo = ""
	}
	return first, failed
}

func (g *Gateway) sendText(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string) (contracts.MessageID, error) {
	if replyTo == "" {
		return g.Post(ctx, conv, text)
	}
	return g.Reply(ctx, conv, replyTo, text)
}

func (g *Gateway) sendBatches(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string, files []openFile) (contracts.MessageID, error) {
	var first contracts.MessageID
	var failed error
	for _, batch := range batchFiles(files, g.batchLimit) {
		sentID, err := g.sendFileBatch(ctx, conv, replyTo, text, batch)
		if err != nil {
			failed = errors.Join(failed, err)
			continue
		}
		first = keepFirst(first, sentID)
		replyTo, text = "", ""
	}
	if text == "" {
		return first, failed
	}
	sentID, err := g.sendParts(ctx, conv, replyTo, []string{text})
	return keepFirst(first, sentID), errors.Join(failed, err)
}

func batchFiles(files []openFile, limit int64) [][]openFile {
	var batches [][]openFile
	var current []openFile
	var size int64
	for _, file := range files {
		if len(current) == filesPerMessage || (len(current) > 0 && size+file.size > limit) {
			batches = append(batches, current)
			current, size = nil, 0
		}
		current = append(current, file)
		size += file.size
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
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
		return msgID(sent), fmt.Errorf("discord send files %s: %w", batchNames(batch), err)
	}
	return msgID(sent), nil
}

func keepFirst(first, next contracts.MessageID) contracts.MessageID {
	if first != "" {
		return first
	}
	return next
}

func batchNames(batch []openFile) string {
	names := make([]string, 0, len(batch))
	for _, opened := range batch {
		names = append(names, opened.file.Name)
	}
	return strings.Join(names, ", ")
}
