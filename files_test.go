package discord

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const refusedLine = "not sent: "

func outboxDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	requireNoError(t, err)
	return dir
}

func writeOutbound(t *testing.T, dir, name, body string) contracts.OutboundFile {
	t.Helper()
	path := filepath.Join(dir, name)
	requireNoError(t, os.WriteFile(path, []byte(body), 0o600))
	return contracts.OutboundFile{Path: path, Name: name, ContentType: "text/plain"}
}

func writeNumbered(t *testing.T, dir string, count int) []contracts.OutboundFile {
	t.Helper()
	files := make([]contracts.OutboundFile, 0, count)
	for index := 1; index <= count; index++ {
		files = append(files, writeOutbound(t, dir, fmt.Sprintf("%02d.txt", index), fmt.Sprintf("%02d", index)))
	}
	return files
}

func noFiles(*testing.T, string) []contracts.OutboundFile {
	return nil
}

func TestSendFiles(t *testing.T) {
	conv := contracts.Conversation{Gateway: "discord", ID: "c1"}
	long := strings.Repeat("a", 1500) + "\n" + strings.Repeat("b", 1500)
	tests := []struct {
		name    string
		replyTo contracts.MessageID
		text    string
		limit   int64
		setup   func(t *testing.T, dir string) []contracts.OutboundFile
		want    []string
		wantID  contracts.MessageID
	}{
		{
			name: "one file without a reply target is posted",
			text: "here",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				return []contracts.OutboundFile{writeOutbound(t, dir, "a.txt", "alpha")}
			},
			want:   []string{`send-files c1 "here" [a.txt=alpha]`},
			wantID: "f1",
		},
		{
			name:    "one file with a reply target is a reply",
			replyTo: "mid",
			text:    "here",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				return []contracts.OutboundFile{writeOutbound(t, dir, "a.txt", "alpha")}
			},
			want:   []string{`reply-files c1 mid "here" [a.txt=alpha]`},
			wantID: "f1",
		},
		{
			name:    "twelve files go in two batches, the text with the first",
			replyTo: "mid",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				return writeNumbered(t, dir, 12)
			},
			want: []string{
				`reply-files c1 mid "" [01.txt=01 02.txt=02 03.txt=03 04.txt=04 05.txt=05 06.txt=06 07.txt=07 08.txt=08 09.txt=09 10.txt=10]`,
				`send-files c1 "" [11.txt=11 12.txt=12]`,
			},
			wantID: "f1",
		},
		{
			name: "a symlink is refused and its target is never sent",
			text: "here",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				target := writeOutbound(t, dir, "real.txt", "secret")
				link := filepath.Join(dir, "link.txt")
				requireNoError(t, os.Symlink(target.Path, link))
				return []contracts.OutboundFile{{Path: link, Name: "link.txt"}, writeOutbound(t, dir, "ok.txt", "fine")}
			},
			want:   []string{`send-files c1 "here\n` + refusedLine + `link.txt" [ok.txt=fine]`},
			wantID: "f1",
		},
		{
			name: "a missing file is refused, the others are still sent",
			text: "here",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				return []contracts.OutboundFile{{Path: filepath.Join(dir, "gone.txt"), Name: "gone.txt"}, writeOutbound(t, dir, "ok.txt", "fine")}
			},
			want:   []string{`send-files c1 "here\n` + refusedLine + `gone.txt" [ok.txt=fine]`},
			wantID: "f1",
		},
		{
			name: "a file under a symlinked directory is refused",
			text: "here",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				outside := outboxDir(t)
				secret := writeOutbound(t, outside, "key", "secret")
				requireNoError(t, os.Symlink(outside, filepath.Join(dir, "d")))
				return []contracts.OutboundFile{{Path: filepath.Join(dir, "d", filepath.Base(secret.Path)), Name: "key"}, writeOutbound(t, dir, "ok.txt", "fine")}
			},
			want:   []string{`send-files c1 "here\n` + refusedLine + `key" [ok.txt=fine]`},
			wantID: "f1",
		},
		{
			name: "a name with a line break is refused",
			text: "here",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				bad := writeOutbound(t, dir, "bad.txt", "x")
				bad.Name = "bad\n.txt"
				return []contracts.OutboundFile{bad, writeOutbound(t, dir, "ok.txt", "fine")}
			},
			want:   []string{`send-files c1 "here\n` + refusedLine + `\"bad\\\\n.txt\"" [ok.txt=fine]`},
			wantID: "f1",
		},
		{
			name: "a directory is refused",
			text: "here",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				sub := filepath.Join(dir, "sub")
				requireNoError(t, os.Mkdir(sub, 0o700))
				return []contracts.OutboundFile{{Path: sub, Name: "sub"}, writeOutbound(t, dir, "ok.txt", "fine")}
			},
			want:   []string{`send-files c1 "here\n` + refusedLine + `sub" [ok.txt=fine]`},
			wantID: "f1",
		},
		{
			name:  "a file over the limit is refused, one at the limit is sent",
			text:  "here",
			limit: 5,
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				return []contracts.OutboundFile{
					writeOutbound(t, dir, "big.txt", "0123456"),
					writeOutbound(t, dir, "small.txt", "abcde"),
				}
			},
			want:   []string{`send-files c1 "here\n` + refusedLine + `big.txt" [small.txt=abcde]`},
			wantID: "f1",
		},
		{
			name:    "a long text is split, its last part goes with the file",
			replyTo: "mid",
			text:    long,
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				return []contracts.OutboundFile{writeOutbound(t, dir, "a.txt", "alpha")}
			},
			want: []string{
				fmt.Sprintf("reply c1 mid %q", strings.Repeat("a", 1500)),
				fmt.Sprintf("send-files c1 %q [a.txt=alpha]", strings.Repeat("b", 1500)),
			},
			wantID: "m2",
		},
		{
			name:    "a long text without files is split, the first part is the reply",
			replyTo: "mid",
			text:    long,
			setup:   noFiles,
			want: []string{
				fmt.Sprintf("reply c1 mid %q", strings.Repeat("a", 1500)),
				fmt.Sprintf("send c1 %q", strings.Repeat("b", 1500)),
			},
			wantID: "m2",
		},
		{
			name:    "no valid file sends the text and the refusal as a reply",
			replyTo: "mid",
			text:    "here",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
				t.Helper()
				target := writeOutbound(t, dir, "real.txt", "secret")
				link := filepath.Join(dir, "link.txt")
				requireNoError(t, os.Symlink(target.Path, link))
				return []contracts.OutboundFile{{Path: link, Name: "link.txt"}}
			},
			want:   []string{`reply c1 mid "here\n` + refusedLine + `link.txt"`},
			wantID: "m2",
		},
		{
			name:  "nothing to send sends nothing",
			setup: noFiles,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{}
			g := NewGateway(fc, testRun(), testMessages(), testLog())
			if tc.limit > 0 {
				g.fileLimit = tc.limit
			}
			id, err := g.SendFiles(context.Background(), conv, tc.replyTo, tc.text, tc.setup(t, outboxDir(t)))
			requireNoError(t, err)
			if id != tc.wantID {
				t.Fatalf("first id = %q, want %q", id, tc.wantID)
			}
			if !slices.Equal(fc.trace, tc.want) {
				t.Fatalf("sent:\n%s\nwant:\n%s", strings.Join(fc.trace, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

func TestSendFilesPropagatesClientErrors(t *testing.T) {
	wantErr := errors.New("discord is down")
	fc := &fakeClient{filesErr: wantErr}
	g := NewGateway(fc, testRun(), testMessages(), testLog())
	file := writeOutbound(t, outboxDir(t), "a.txt", "alpha")
	_, err := g.SendFiles(context.Background(), contracts.Conversation{ID: "c1"}, "", "here", []contracts.OutboundFile{file})
	if !errors.Is(err, wantErr) {
		t.Fatalf("SendFiles error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestSendFilesRefusesFilesPastTheFiftieth(t *testing.T) {
	fc := &fakeClient{}
	g := NewGateway(fc, testRun(), testMessages(), testLog())
	files := writeNumbered(t, outboxDir(t), maxFilesPerSend+2)
	_, err := g.SendFiles(context.Background(), contracts.Conversation{Gateway: "discord", ID: "c1"}, "", "", files)
	requireNoError(t, err)
	if len(fc.trace) != maxFilesPerSend/filesPerMessage {
		t.Fatalf("messages = %d, want %d", len(fc.trace), maxFilesPerSend/filesPerMessage)
	}
	if want := refusedLine + "51.txt, 52.txt"; !strings.Contains(fc.trace[0], want) {
		t.Fatalf("first message = %q, want it to list %q", fc.trace[0], want)
	}
}

func TestSendFilesKeepsSendingAfterAFailedBatch(t *testing.T) {
	wantErr := errors.New("discord is down")
	fc := &fakeClient{filesErr: wantErr, filesErrAt: 2}
	g := NewGateway(fc, testRun(), testMessages(), testLog())
	files := writeNumbered(t, outboxDir(t), 3*filesPerMessage)
	id, err := g.SendFiles(context.Background(), contracts.Conversation{Gateway: "discord", ID: "c1"}, "", "here", files)
	if !errors.Is(err, wantErr) {
		t.Fatalf("SendFiles error = %v, want it to wrap %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "11.txt") {
		t.Fatalf("SendFiles error = %v, want it to name the files not sent", err)
	}
	if id != "f1" || len(fc.trace) != 2 {
		t.Fatalf("id = %q, messages = %d, want f1 and the first and third batches", id, len(fc.trace))
	}
}

func TestSendFilesSplitsBatchesByTotalSize(t *testing.T) {
	fc := &fakeClient{}
	g := NewGateway(fc, testRun(), testMessages(), testLog())
	g.batchLimit = 5
	dir := outboxDir(t)
	files := []contracts.OutboundFile{writeOutbound(t, dir, "a.txt", "abc"), writeOutbound(t, dir, "b.txt", "def"), writeOutbound(t, dir, "c.txt", "gh")}
	_, err := g.SendFiles(context.Background(), contracts.Conversation{Gateway: "discord", ID: "c1"}, "", "", files)
	requireNoError(t, err)
	want := []string{`send-files c1 "" [a.txt=abc]`, `send-files c1 "" [b.txt=def c.txt=gh]`}
	if !slices.Equal(fc.trace, want) {
		t.Fatalf("trace = %q, want %q", fc.trace, want)
	}
}

func TestSendFilesListsTwentyRefusedNamesAtMost(t *testing.T) {
	fc := &fakeClient{}
	g := NewGateway(fc, testRun(), testMessages(), testLog())
	files := make([]contracts.OutboundFile, 0, maxRefusedListed+3)
	for index := range maxRefusedListed + 3 {
		files = append(files, contracts.OutboundFile{Path: filepath.Join(outboxDir(t), "missing"), Name: fmt.Sprintf("m%02d", index)})
	}
	_, err := g.SendFiles(context.Background(), contracts.Conversation{Gateway: "discord", ID: "c1"}, "", "", files)
	requireNoError(t, err)
	if len(fc.trace) != 1 || !strings.HasSuffix(fc.trace[0], `m19 and 3 more"`) {
		t.Fatalf("trace = %q, want one notice naming twenty files and counting three more", fc.trace)
	}
}

func TestSendFilesCarriesTheTextToTheNextBatchWhenTheFirstFails(t *testing.T) {
	wantErr := errors.New("discord is down")
	fc := &fakeClient{filesErr: wantErr, filesErrAt: 1}
	g := NewGateway(fc, testRun(), testMessages(), testLog())
	files := writeNumbered(t, outboxDir(t), filesPerMessage+1)
	id, err := g.SendFiles(context.Background(), contracts.Conversation{Gateway: "discord", ID: "c1"}, "", "here", files)
	if !errors.Is(err, wantErr) {
		t.Fatalf("SendFiles error = %v, want it to wrap %v", err, wantErr)
	}
	if want := []string{`send-files c1 "here" [11.txt=11]`}; id != "f2" || !slices.Equal(fc.trace, want) {
		t.Fatalf("id = %q, trace = %q, want f2 and %q", id, fc.trace, want)
	}
}

func TestSendFilesPostsTheTextAloneWhenEveryBatchFails(t *testing.T) {
	wantErr := errors.New("discord is down")
	fc := &fakeClient{filesErr: wantErr}
	g := NewGateway(fc, testRun(), testMessages(), testLog())
	files := writeNumbered(t, outboxDir(t), 2)
	_, err := g.SendFiles(context.Background(), contracts.Conversation{Gateway: "discord", ID: "c1"}, "", "here", files)
	if !errors.Is(err, wantErr) {
		t.Fatalf("SendFiles error = %v, want it to wrap %v", err, wantErr)
	}
	if want := []string{`send c1 "here"`}; !slices.Equal(fc.trace, want) {
		t.Fatalf("trace = %q, want %q", fc.trace, want)
	}
}

func TestSendFilesKeepsSendingTextPartsAfterAFailedOne(t *testing.T) {
	wantErr := errors.New("discord is down")
	fc := &fakeClient{sendErr: wantErr, sendErrAt: 1}
	g := NewGateway(fc, testRun(), testMessages(), testLog())
	text := strings.Repeat("a", gatewayMaxLen) + strings.Repeat("b", gatewayMaxLen) + "c"
	id, err := g.SendFiles(context.Background(), contracts.Conversation{Gateway: "discord", ID: "c1"}, "", text, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("SendFiles error = %v, want it to wrap %v", err, wantErr)
	}
	if id != "m1" || len(fc.sent) != 2 || fc.sent[1].content != "c" {
		t.Fatalf("id = %q, sent = %d parts, want m1 and the two parts after the failed one", id, len(fc.sent))
	}
}

func TestSendFilesRefusesBadlyFormedFiles(t *testing.T) {
	dir := outboxDir(t)
	cases := []struct {
		name string
		file func() contracts.OutboundFile
		want string
	}{
		{name: "an empty name", file: func() contracts.OutboundFile { f := writeOutbound(t, dir, "e.txt", "x"); f.Name = ""; return f }, want: `\"\"`},
		{name: "an invalid UTF-8 name", file: func() contracts.OutboundFile {
			f := writeOutbound(t, dir, "u.txt", "x")
			f.Name = "u\xff.txt"
			return f
		}, want: `\"u\\\\xff.txt\"`},
		{name: "an invalid content type", file: func() contracts.OutboundFile {
			f := writeOutbound(t, dir, "c.txt", "x")
			f.ContentType = "text/plain; charset"
			return f
		}, want: "c.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{}
			g := NewGateway(fc, testRun(), testMessages(), testLog())
			ok := writeOutbound(t, dir, "ok.txt", "fine")
			_, err := g.SendFiles(context.Background(), contracts.Conversation{Gateway: "discord", ID: "c1"}, "", "", []contracts.OutboundFile{tc.file(), ok})
			requireNoError(t, err)
			if want := []string{`send-files c1 "` + refusedLine + tc.want + `" [ok.txt=fine]`}; !slices.Equal(fc.trace, want) {
				t.Fatalf("trace = %q, want %q", fc.trace, want)
			}
		})
	}
}
