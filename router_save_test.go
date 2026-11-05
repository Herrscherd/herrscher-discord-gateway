package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestUndeliverableBufferedMessageIsReported(t *testing.T) {
	r, ctrl, _, f := newTestRouterRendering(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	r.onMessage(context.Background(), ownerPing("fix the login bug"))
	ctrl.refuseSubmit = true

	r.onBindPick(context.Background(), "c1", "", "local:herrscher")

	if len(f.posts) != 1 || !strings.Contains(f.posts[0], "I could not pass your message to the session, send it again") {
		t.Fatalf("the operator was not told the message was lost: posts=%q", f.posts)
	}
}

func TestBindSaveFailureReportsInTheChannel(t *testing.T) {
	cases := map[string]struct {
		breakStore func(*bindStore)
		wantMsg    string
	}{
		"locked store": {
			breakStore: func(b *bindStore) { b.locked = fmt.Errorf("%w: corrupt", errStoreLocked) },
			wantMsg:    "unreadable",
		},
		"write error": {
			breakStore: func(b *bindStore) {
				b.write = func(string, []byte) error { return errors.New("disk full") }
			},
			wantMsg: "could not be written",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, ctrl, _, f := newTestRouterRendering(t)
			ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
			r.onMessage(context.Background(), ownerPing("fix the login bug"))
			tc.breakStore(r.binds)

			r.onBindPick(context.Background(), "c1", "", "local:herrscher")

			if len(f.posts) == 0 || !strings.Contains(f.posts[0], tc.wantMsg) {
				t.Fatalf("the channel was not told the save failed: posts=%q", f.posts)
			}
		})
	}
}
