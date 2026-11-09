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

const refusedLine = "not sent, too large or not a regular file: "

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
				return []contracts.OutboundFile{writeOutbound(t, dir, "a.txt", "alpha")}
			},
			want:   []string{`reply-files c1 mid "here" [a.txt=alpha]`},
			wantID: "f1",
		},
		{
			name:    "twelve files go in two batches, the text with the first",
			replyTo: "mid",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
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
				target := writeOutbound(t, dir, "real.txt", "secret")
				link := filepath.Join(dir, "link.txt")
				requireNoError(t, os.Symlink(target.Path, link))
				return []contracts.OutboundFile{{Path: link, Name: "link.txt"}, writeOutbound(t, dir, "ok.txt", "fine")}
			},
			want:   []string{`send-files c1 "here\n` + refusedLine + `link.txt" [ok.txt=fine]`},
			wantID: "f1",
		},
		{
			name: "a directory is refused",
			text: "here",
			setup: func(t *testing.T, dir string) []contracts.OutboundFile {
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
			id, err := g.SendFiles(context.Background(), conv, tc.replyTo, tc.text, tc.setup(t, t.TempDir()))
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
	file := writeOutbound(t, t.TempDir(), "a.txt", "alpha")
	_, err := g.SendFiles(context.Background(), contracts.Conversation{ID: "c1"}, "", "here", []contracts.OutboundFile{file})
	if !errors.Is(err, wantErr) {
		t.Fatalf("SendFiles error = %v, want it to wrap %v", err, wantErr)
	}
}
