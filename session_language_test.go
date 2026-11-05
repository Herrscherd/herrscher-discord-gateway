package discord

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

func sessionLanguageOf(ctrl *fakeCtrl, channel string, lang i18n.Language) {
	ctrl.sessions = []contracts.SessionInfo{{Name: "ch-" + channel, ChannelID: channel, Language: lang}}
}

func sentTo(c *fakeClient, channel, content string) bool {
	for _, m := range c.sent {
		if m.channel == channel && m.content == content {
			return true
		}
	}
	return false
}

func supportThreadDenied(t *testing.T, language i18n.Language) (*router, *fakeCtrl, *fakeClient) {
	t.Helper()
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.publicErr = errors.New("403: Missing Permissions")
	if err := r.binds.SetMode("c1", supportMode); err != nil {
		t.Fatal(err)
	}
	sessionLanguageOf(ctrl, "c1", language)
	return r, ctrl, c
}

func TestSessionChannelNoticesSpeakTheSessionLanguage(t *testing.T) {
	r, _, c := supportThreadDenied(t, i18n.French)

	r.onMessage(context.Background(), supportPing("m1", "corrige herrscher"))

	if want := frenchReply(t, "thread.public_denied"); !sentTo(c, "c1", want) {
		t.Fatalf("sent = %+v, want %q in the session's French", c.sent, want)
	}
}

func TestUnknownSessionLanguageFallsBackToTheOperatorLanguage(t *testing.T) {
	r, _, c := supportThreadDenied(t, 0)
	r.msgs = testMessages().in(i18n.French)

	r.onMessage(context.Background(), supportPing("m1", "corrige herrscher"))

	if want := frenchReply(t, "thread.public_denied"); !sentTo(c, "c1", want) {
		t.Fatalf("sent = %+v, want the operator's French %q", c.sent, want)
	}
}

func TestSessionLanguageBeatsTheOperatorLanguage(t *testing.T) {
	r, _, c := supportThreadDenied(t, i18n.English)
	r.msgs = testMessages().in(i18n.French)

	r.onMessage(context.Background(), supportPing("m1", "corrige herrscher"))

	want := testMessages().sayIn(i18n.English, "thread.public_denied", nil)
	if !sentTo(c, "c1", want) {
		t.Fatalf("sent = %+v, want the session's English %q", c.sent, want)
	}
}

func TestEphemeralRepliesInASessionChannelFollowTheInteractionLocale(t *testing.T) {
	tr := &recordingTransport{}
	s := interactionSlash(t, tr)
	r, ctrl, _ := boundRouter(t)
	sessionLanguageOf(ctrl, "c1", i18n.English)
	s.router, s.ctrl = r, ctrl
	ix := frenchInteraction(dctl.InteractionOption{Name: "mode", Type: 3, Value: "off"})
	ix.ChannelID = "c1"

	if _, err := s.handleMode(context.Background(), ix); err != nil {
		t.Fatal(err)
	}
	requireReplies(t, tr, frenchReply(t, "mode.off"))
}

func TestNotifyRendersInTheSessionLanguage(t *testing.T) {
	tr := &recordingTransport{}
	bot := dctl.New("t", "chan", dctl.WithHTTPClient(&http.Client{Transport: tr}), dctl.WithGlobalCommands())
	ctrl := newFakeCtrl()
	sessionLanguageOf(ctrl, "ch-1", i18n.French)
	g := NewGateway(discordClient{bot}, testRun(), testMessages(), testLog())
	g.langs = sessionLanguages{
		ctrl:  func() contracts.SessionControl { return ctrl },
		binds: newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog()),
	}
	notice := contracts.Notice{Code: contracts.CodeSessionCreated, Args: []contracts.Arg{contracts.Text("Name", "alpha")}}

	if _, err := g.Notify(context.Background(), contracts.Conversation{ID: "ch-1"}, i18n.English, notice); err != nil {
		t.Fatal(err)
	}

	want, err := testMessages().in(i18n.French).notice(i18n.French, notice)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.replies()
	if len(got) != 1 || !strings.Contains(got[0], want) {
		t.Fatalf("posted = %q, want the French notice %q", got, want)
	}
}

func TestSinkProgressSpeaksTheSessionLanguage(t *testing.T) {
	f := &fakeRender{}
	ctrl := newFakeCtrl()
	sessionLanguageOf(ctrl, "chanA", i18n.French)
	set := newSinks(testRun(), f, staticLevels("full"), "", false, testMessages(), testLog())
	set.langs = sessionLanguages{
		ctrl:  func() contracts.SessionControl { return ctrl },
		binds: newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog()),
	}

	set.at("chanA").handle(contracts.Event{T: "reply", Text: "", Done: true})

	french := testMessages().in(i18n.French)
	want := withEmoji(french.head(headDone), french.say("progress.done", nil))
	if got := f.postsTo("chanA"); len(got) != 1 || got[0] != want {
		t.Fatalf("posts = %q, want the French summary %q", got, want)
	}
}
