package discord

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	maxFileBytes    int64 = 8 << 20
	filesPerMessage       = 10
	namesArg              = "Names"
)

var errFileRefused = errors.New("file not sent")

type openFile struct {
	file dctl.File
	src  *os.File
}

// SendFiles posts text in reply to replyTo in conv, with files attached, at most ten
// files to a message. A file that is not a regular file, or that is larger than the
// size limit, is not sent: its name is listed at the end of the text. It returns the
// id of the first message sent, and an error that wraps the failure. It satisfies
// contracts.FileSender.
func (g *Gateway) SendFiles(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string, files []contracts.OutboundFile) (sent contracts.MessageID, err error) {
	ready, refused, openErr := g.openFiles(files)
	defer func() { err = errors.Join(err, closeFiles(ready)) }()
	if openErr != nil {
		return "", openErr
	}
	parts := chunkText(g.withRefusals(text, refused), gatewayMaxLen)
	if len(ready) == 0 {
		return g.sendParts(ctx, conv, replyTo, parts)
	}
	lead, last := splitLast(parts)
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

func (g *Gateway) openFiles(files []contracts.OutboundFile) ([]openFile, []string, error) {
	ready := make([]openFile, 0, len(files))
	var refused []string
	for _, out := range files {
		opened, err := openOutbound(out, g.fileLimit)
		switch {
		case errors.Is(err, errFileRefused):
			g.log.Warn("outbound file not sent", "file", out.Name, "err", err)
			refused = append(refused, out.Name)
		case err != nil:
			return ready, refused, err
		default:
			ready = append(ready, opened)
		}
	}
	return ready, refused, nil
}

func openOutbound(out contracts.OutboundFile, limit int64) (openFile, error) {
	info, err := os.Lstat(out.Path)
	if err != nil {
		return openFile{}, fmt.Errorf("discord file %q: %w", out.Name, err)
	}
	if !info.Mode().IsRegular() {
		return openFile{}, fmt.Errorf("%w: %q is not a regular file", errFileRefused, out.Name)
	}
	src, err := os.Open(out.Path)
	if err != nil {
		return openFile{}, fmt.Errorf("discord file %q: %w", out.Name, err)
	}
	if err = checkOpened(src, out.Name, limit); err != nil {
		return openFile{}, errors.Join(err, src.Close())
	}
	return openFile{file: dctl.File{Name: out.Name, Content: src, ContentType: out.ContentType}, src: src}, nil
}

func checkOpened(src *os.File, name string, limit int64) error {
	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("discord file %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %q is not a regular file", errFileRefused, name)
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
		id, err := g.sendText(ctx, conv, replyTo, part)
		if err != nil {
			return first, err
		}
		first = keepFirst(first, id)
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
		id, err := g.sendFileBatch(ctx, conv, replyTo, content, files[start:end])
		if err != nil {
			return first, err
		}
		first = keepFirst(first, id)
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

func splitLast(parts []string) ([]string, string) {
	if len(parts) == 0 {
		return nil, ""
	}
	last := len(parts) - 1
	return parts[:last], parts[last]
}

func keepFirst(first, next contracts.MessageID) contracts.MessageID {
	if first != "" {
		return first
	}
	return next
}
