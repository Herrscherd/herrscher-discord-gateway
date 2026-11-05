package discord

import (
	"context"
	"errors"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

func englishHead(key headKey) string {
	return testMessages().headIn(i18n.English, key)
}

func TestEveryHeadKeyIsDeclaredInTheEnglishTable(t *testing.T) {
	english := testMessages().heads[i18n.English]
	keys := declaredHeadKeys(t)
	if len(keys) == 0 {
		t.Fatal("no head key found in the production code")
	}
	for _, key := range keys {
		if _, ok := english[key]; !ok {
			t.Errorf("head key %q is not in the English table", key)
		}
	}
}

func declaredHeadKeys(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	var keys []string
	for _, file := range productionFiles(t, fset) {
		ast.Inspect(file, func(node ast.Node) bool {
			spec, ok := node.(*ast.ValueSpec)
			if !ok || !isHeadKeyType(spec.Type) {
				return true
			}
			for _, value := range spec.Values {
				if lit, isLit := value.(*ast.BasicLit); isLit {
					keys = append(keys, unquoteLiteral(t, lit))
				}
			}
			return true
		})
	}
	return keys
}

func unquoteLiteral(t *testing.T, lit *ast.BasicLit) string {
	t.Helper()
	key, err := strconv.Unquote(lit.Value)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func isHeadKeyType(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "headKey"
}

func TestEveryNoticeCodeHasAnEnglishHead(t *testing.T) {
	english := testMessages().heads[i18n.English]
	for _, code := range contracts.Codes() {
		if _, ok := english[string(noticeHead(code))]; !ok {
			t.Errorf("no English head for notice code %s", code.Key())
		}
	}
}

func TestNoEmojiLiteralOutsideTheHeadTables(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range string(data) {
			if isEmojiRune(r) {
				t.Errorf("%s holds the emoji rune %U, heads belong in heads/*.json", name, r)
				break
			}
		}
	}
}

func isEmojiRune(r rune) bool {
	switch {
	case r == 0xFE0F, r >= 0x2300 && r <= 0x23FF, r >= 0x2600 && r <= 0x27BF,
		r >= 0x2B00 && r <= 0x2BFF, r >= 0x1F000 && r <= 0x1FAFF:
		return true
	}
	return false
}

func TestHeadFallsBackFromFrenchToEnglishThenEmpty(t *testing.T) {
	tables, err := loadHeads(fstest.MapFS{
		"en.json": {Data: []byte(`[{"id":"ack","other":"A"},{"id":"fail","other":"F"},{"id":"done","other":""},{"id":"stop","other":"S"}]`)},
		"fr.json": {Data: []byte(`[{"id":"ack","other":"a"},{"id":"stop","other":""}]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := messages{heads: tables, lang: i18n.French}
	tests := []struct {
		name string
		lang i18n.Language
		key  headKey
		want string
	}{
		{"French own head", i18n.French, headAck, "a"},
		{"French falls back to English", i18n.French, headFail, "F"},
		{"English empty head stays empty", i18n.French, headDone, ""},
		{"explicit empty French head is kept", i18n.French, headStop, ""},
		{"absent everywhere is empty", i18n.French, headThinking, ""},
		{"English own head", i18n.English, headAck, "A"},
		{"English absent is empty", i18n.English, headThinking, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := msgs.headIn(tc.lang, tc.key); got != tc.want {
				t.Errorf("headIn(%s, %s) = %q, want %q", tc.lang, tc.key, got, tc.want)
			}
		})
	}
}

func TestEmbeddedFrenchAndEnglishTablesCoverTheSameKeys(t *testing.T) {
	tables := testMessages().heads
	english, french := tables[i18n.English], tables[i18n.French]
	if len(french) != len(english) {
		t.Errorf("fr has %d heads, en has %d", len(french), len(english))
	}
	for key := range english {
		if _, ok := french[key]; !ok {
			t.Errorf("fr has no head for %s", key)
		}
	}
}

func TestHeadsFileOverridesEveryLanguage(t *testing.T) {
	path := writeHeadsFile(t, `{"ack":"A","session.created":""}`)
	base := testMessages()
	msgs, err := base.withHeadsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []i18n.Language{i18n.English, i18n.French} {
		if got := msgs.headIn(lang, headAck); got != "A" {
			t.Errorf("%s ack head = %q, want the override A", lang, got)
		}
		if got := msgs.headIn(lang, noticeHead(contracts.CodeSessionCreated)); got != "" {
			t.Errorf("%s session.created head = %q, want the empty override", lang, got)
		}
	}
	if got := base.headIn(i18n.English, headAck); got != englishHead(headAck) {
		t.Errorf("the override leaked into the base messages: ack = %q", got)
	}
}

func TestHeadsFileEmptyValueDisablesAHeadInTheNotice(t *testing.T) {
	path := writeHeadsFile(t, `{"compaction.done":""}`)
	msgs, err := testMessages().withHeadsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeClient{}
	g := NewGateway(f, testRun(), msgs, testLog())
	if _, err := g.Notify(context.Background(), contracts.Conversation{Gateway: gatewayKind, ID: "c1"}, i18n.English, contracts.Notice{Code: contracts.CodeCompacted}); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0].content != "context compacted" {
		t.Fatalf("sent %+v, want the notice with no head", f.sent)
	}
}

func TestHeadsFileEmptyPathChangesNothing(t *testing.T) {
	base := testMessages()
	msgs, err := base.withHeadsFile("")
	if err != nil {
		t.Fatal(err)
	}
	if got := msgs.headIn(i18n.English, headAck); got != englishHead(headAck) {
		t.Errorf("ack head = %q, want the built-in %q", got, englishHead(headAck))
	}
}

func TestHeadsFileRefusesAnUnknownKeyAsASettingError(t *testing.T) {
	path := writeHeadsFile(t, `{"acknowledge":"A"}`)
	_, err := testMessages().withHeadsFile(path)
	if !errors.Is(err, errUnknownHead) {
		t.Fatalf("err = %v, want an unknown head key", err)
	}
	assertSettingInvalid(t, err, path)
}

func TestHeadsFileRefusesAnUnreadableOrInvalidFile(t *testing.T) {
	invalid := writeHeadsFile(t, `{"ack": 1}`)
	tests := map[string]string{
		"missing file": filepath.Join(t.TempDir(), "absent.json"),
		"invalid json": invalid,
	}
	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := testMessages().withHeadsFile(path)
			if err == nil {
				t.Fatal("no error for an unusable heads file")
			}
			assertSettingInvalid(t, err, path)
		})
	}
}

func assertSettingInvalid(t *testing.T, err error, path string) {
	t.Helper()
	var noticed contracts.NoticeError
	if !errors.As(err, &noticed) {
		t.Fatalf("err = %v, want a notice error", err)
	}
	notice := noticed.Notice()
	if notice.Code != contracts.CodeSettingInvalid {
		t.Fatalf("notice code = %s, want %s", notice.Code.Key(), contracts.CodeSettingInvalid.Key())
	}
	if got := contracts.NoticeOf(err); got.Code != contracts.CodeSettingInvalid {
		t.Errorf("NoticeOf code = %s, want setting.invalid", got.Code.Key())
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file %q", err, path)
	}
}

func writeHeadsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "heads.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDisabledAckHeadAddsNoReaction(t *testing.T) {
	msgs, err := testMessages().withHeadsFile(writeHeadsFile(t, `{"ack":""}`))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRender{channel: "c1"}
	s := newSink(testRun(), f, "c1", staticLevel("full"), "", false, msgs, testLog())
	s.noteUser(context.Background(), "c1", "u1", "42")
	s.handle(contracts.Event{T: "human", Who: "alice", Text: "hi"})
	s.handle(contracts.Event{T: "reply", Text: "done", Done: true})
	if len(f.reacted) != 0 || len(f.unreacted) != 0 {
		t.Fatalf("reacted = %v, unreacted = %v, want no reaction with the ack head off", f.reacted, f.unreacted)
	}
	if len(f.posts) != 1 || f.posts[0] != "done" {
		t.Fatalf("posts = %v, want [done]", f.posts)
	}
}

func TestGatewayMessagesAppliesTheHeadsSetting(t *testing.T) {
	path := writeHeadsFile(t, `{"ack":"A"}`)
	msgs, err := gatewayMessages(contracts.PluginConfig{Settings: map[string]string{headsSetting: path}}, testLog())
	if err != nil {
		t.Fatal(err)
	}
	if got := msgs.headIn(i18n.English, headAck); got != "A" {
		t.Errorf("ack head = %q, want the override A from the heads setting", got)
	}
}

func TestGatewayMessagesWithoutHeadsSettingKeepsTheDefaults(t *testing.T) {
	msgs, err := gatewayMessages(contracts.PluginConfig{}, testLog())
	if err != nil {
		t.Fatal(err)
	}
	if got := msgs.headIn(i18n.English, headAck); got != englishHead(headAck) {
		t.Errorf("ack head = %q, want the built-in head", got)
	}
}

func TestFrenchHeadsArePinned(t *testing.T) {
	french := testMessages().heads[i18n.French]
	pinned := map[headKey]string{
		headAck:      "⏳",
		headFail:     "❌",
		headDone:     "✅",
		headToolRead: "📖",
		headToolEdit: "✏️",
	}
	for key, want := range pinned {
		if got := french[string(key)]; got != want {
			t.Errorf("fr head %s = %q, want %q", key, got, want)
		}
	}
}
