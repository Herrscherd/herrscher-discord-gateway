package discord

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
	"github.com/Herrscherd/herrscher-contracts/i18n/i18ntest"
)

func testMessages() messages {
	msgs, err := newMessages(i18n.English, testLog())
	if err != nil {
		panic(err)
	}
	return msgs
}

func declaredMessages() map[string][]string {
	return map[string][]string{
		"help.channel_read":     {},
		"help.channel_post":     {},
		"help.message_reply":    {},
		"help.message_react":    {},
		"help.message_unreact":  {},
		"help.message_edit":     {},
		"help.message_delete":   {},
		"param.channel_id":      {},
		"param.limit":           {},
		"param.after":           {},
		"param.text":            {},
		"param.reply_to":        {},
		"param.message_id":      {},
		"param.emoji_add":       {},
		"param.emoji_remove":    {},
		"param.new_body":        {},
		"command.no_messages":   {},
		"command.attachments":   {"Count", "Name"},
		"undelivered":           {},
		"repo.none":             {},
		"repo.reused":           {"Repo"},
		"repo.ask":              {},
		"repo.ask_capped":       {"Count", "Question", "Shown"},
		"repo.menu_failed":      {},
		"repo.started":          {"Repo"},
		"bindings.write_failed": {},
		"bindings.unreadable":   {},
		"bind.bad_choice":       {},
		"bind.create_failed":    {"Detail"},
		"control.unavailable":   {},
		"menu.owned_by":         {"Mention"},
		"menu.denied":           {},
		"menu.not_allowed":      {},
		"choice.gone":           {},
		"choice.saved":          {"Value"},
		"thread.public_denied":  {},
		"thread.private_opened": {"Channel"},
		"thread.private_denied": {},
		"stop.none":             {},
		"stop.done":             {},
		"store.unavailable":     {},
		"verbosity.unknown":     {"Level"},
		"verbosity.set":         {"Level"},
		"mode.unknown":          {"Mode"},
		"mode.ambient_refused":  {},
		"mode.support":          {},
		"mode.ambient":          {},
		"mode.off":              {},
		"mode.normal":           {},
		"gate.denied":           {},
		"run.done":              {},
		"save.lost":             {},
		"save.unreadable":       {},
		"allow.session_added":   {"Mention", "Name"},
		"allow.session_removed": {"Mention", "Name"},
		"allow.global_added":    {"Mention"},
		"allow.global_removed":  {"Mention"},
		"allow.label_session":   {"Name"},
		"allow.label_global":    {},
		"allow.empty":           {"Label"},
		"allow.list":            {"Label", "Mentions"},
		"progress.running":      {},
		"progress.done":         {},
		"progress.actions":      {"Count"},
		"notice.unexpected":     {},
	}
}

func TestGatewayCatalogIsComplete(t *testing.T) {
	sources, err := fs.Sub(localeFS, "locales")
	if err != nil {
		t.Fatal(err)
	}
	i18ntest.CheckDeclared(t, declaredMessages(), sources)
}

var sayCall = regexp.MustCompile(`\b(?:say|sayIn|Help|Param|ValueParam)\(\s*(?:[^,()]+,\s*)?"([^"]+)"`)

func TestEveryKeyTheCodeUsesIsDeclared(t *testing.T) {
	used := make(map[string]bool)
	for _, src := range goSources(t) {
		for _, match := range sayCall.FindAllStringSubmatch(src, -1) {
			used[match[1]] = true
			if _, declared := declaredMessages()[match[1]]; !declared {
				t.Errorf("key %q is used by the code but not declared", match[1])
			}
		}
	}
	for key := range declaredMessages() {
		if !used[key] {
			t.Errorf("key %q is declared but no code uses it", key)
		}
	}
}

func goSources(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(data))
	}
	return out
}

func TestNoticeRendersInEachLanguage(t *testing.T) {
	msgs := testMessages()
	notice := contracts.Notice{Code: contracts.CodeSessionCreated, Args: []contracts.Arg{contracts.Text("Name", "alpha")}}
	tests := []struct {
		lang i18n.Language
		want string
	}{
		{i18n.English, "session alpha created"},
		{i18n.French, "session « alpha » créée"},
	}
	for _, tc := range tests {
		got, err := msgs.notice(tc.lang, notice)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.lang, got, tc.want)
		}
	}
}

func TestRefArgumentRendersAsChannelMention(t *testing.T) {
	msgs := testMessages()
	notice := contracts.Notice{Code: contracts.CodeSessionCreated, Args: []contracts.Arg{contracts.Ref("Name", "123")}}
	got, err := msgs.notice(i18n.English, notice)
	if err != nil {
		t.Fatal(err)
	}
	if got != "session <#123> created" {
		t.Errorf("got %q", got)
	}
}

func TestUnexpectedDetailIsShownInANeutralisedCodeBlock(t *testing.T) {
	msgs := testMessages()
	notice := contracts.NoticeOf(testError("boom ``` @everyone"))
	got, err := msgs.notice(i18n.French, notice)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "```"); n != 2 {
		t.Errorf("got %d code fences in %q, want the opening and closing pair", n, got)
	}
	if strings.Contains(got, "@everyone") {
		t.Errorf("raw mention survives in %q", got)
	}
	if !strings.Contains(got, "@\u200beveryone") {
		t.Errorf("mention not neutralised in %q", got)
	}
	if !strings.HasPrefix(got, "une erreur interne s'est produite") {
		t.Errorf("intro not translated in %q", got)
	}
}

func TestUnexpectedDetailIsNotTranslated(t *testing.T) {
	msgs := testMessages()
	notice := contracts.NoticeOf(testError("connection refused"))
	got, err := msgs.notice(i18n.French, notice)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "```\nconnection refused\n```") {
		t.Errorf("detail not shown verbatim in a code block: %q", got)
	}
}

type testError string

func (e testError) Error() string { return string(e) }

func TestMessageFallsBackToEnglishWhenLanguageLacksKey(t *testing.T) {
	catalog, err := i18n.NewCatalog(testLog(), fstest.MapFS{
		"en.json": {Data: []byte(`[{"id":"progress.done","other":"done"}]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := messages{catalog: catalog, lang: i18n.French, log: testLog()}
	if got := msgs.say("progress.done", nil); got != "done" {
		t.Errorf("got %q, want the English text", got)
	}
}

func TestPluralFormFollowsCount(t *testing.T) {
	msgs := testMessages()
	tests := []struct {
		lang  i18n.Language
		count int
		want  string
	}{
		{i18n.English, 1, "1 action"},
		{i18n.English, 3, "3 actions"},
		{i18n.French, 1, "1 action"},
		{i18n.French, 3, "3 actions"},
	}
	for _, tc := range tests {
		got := msgs.sayIn(tc.lang, "progress.actions", map[string]any{"Count": tc.count})
		if got != tc.want {
			t.Errorf("%s %d: got %q, want %q", tc.lang, tc.count, got, tc.want)
		}
	}
}

func TestZeroLanguageResolvesToOperatorDefault(t *testing.T) {
	msgs, err := newMessages(i18n.French, testLog())
	if err != nil {
		t.Fatal(err)
	}
	if got := msgs.resolve(0); got != i18n.French {
		t.Errorf("resolve(0) = %s, want the operator default fr", got)
	}
	if got := msgs.resolve(i18n.English); got != i18n.English {
		t.Errorf("resolve(en) = %s, want en", got)
	}
	if got := msgs.say("progress.done", nil); got != "terminé" {
		t.Errorf("default rendering = %q, want the French text", got)
	}
}

func TestNotifyPostsTheNoticeInTheGivenLanguage(t *testing.T) {
	f := &fakeClient{}
	g := NewGateway(f, testRun(), testMessages(), testLog())
	notice := contracts.Notice{Code: contracts.CodeSessionCreated, Args: []contracts.Arg{contracts.Text("Name", "alpha")}}
	if _, err := g.Notify(context.Background(), contracts.Conversation{Gateway: gatewayKind, ID: "c1"}, i18n.French, notice); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0].content != "✅ session « alpha » créée" {
		t.Fatalf("sent %+v, want one French notice", f.sent)
	}
}

func runText(ctx context.Context, cmd contracts.Cmd, in contracts.Input) (string, error) {
	res, err := cmd.Run(ctx, in)
	if err != nil {
		return "", err
	}
	text, ok := res.Data().(string)
	if !ok {
		return "", fmt.Errorf("result data is %T, want a string", res.Data())
	}
	return text, nil
}
