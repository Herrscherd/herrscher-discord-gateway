package discord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
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
		"notice.no_detail":      {},
		"channel.unknown":       {},
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

type keyCall struct {
	where   token.Position
	name    string
	key     string
	literal bool
}

func TestEveryKeyTheCodeUsesIsDeclared(t *testing.T) {
	fset := token.NewFileSet()
	files := productionFiles(t, fset)
	used := make(map[string]bool)
	for _, call := range keyCalls(fset, files, stringConstants(files)) {
		if !call.literal {
			t.Errorf("%s: the key of %s is neither a string literal nor a string constant", call.where, call.name)
			continue
		}
		used[call.key] = true
		if _, declared := declaredMessages()[call.key]; !declared {
			t.Errorf("%s: key %q is used by the code but not declared", call.where, call.key)
		}
	}
	for key := range declaredMessages() {
		if !used[key] {
			t.Errorf("key %q is declared but no code uses it", key)
		}
	}
}

func productionFiles(t *testing.T, fset *token.FileSet) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Clean(name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	return files
}

func keyCalls(fset *token.FileSet, files []*ast.File, consts map[string]string) []keyCall {
	var calls []keyCall
	for _, file := range files {
		for _, decl := range file.Decls {
			calls = append(calls, callsIn(fset, decl, consts)...)
		}
	}
	return calls
}

func callsIn(fset *token.FileSet, decl ast.Decl, consts map[string]string) []keyCall {
	if isKeyFunnel(decl) {
		return nil
	}
	argIndex := map[string]int{"say": 0, "sayIn": 1, "Help": 0, "Param": 1, "ValueParam": 1, "catalogText": 1, "Has": 0, "Message": 1}
	var calls []keyCall
	ast.Inspect(decl, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if found, isKeyCall := keyCallOf(fset, call, argIndex, consts); isKeyCall {
				calls = append(calls, found)
			}
		}
		return true
	})
	return calls
}

func isKeyFunnel(decl ast.Decl) bool {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	switch fn.Name.Name {
	case "say", "sayIn", "catalogText":
	default:
		return false
	}
	return receiverName(fn.Recv.List[0].Type) == "messages"
}

func receiverName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func keyCallOf(fset *token.FileSet, call *ast.CallExpr, argIndex map[string]int, consts map[string]string) (keyCall, bool) {
	name := calleeName(call)
	index, isKeyCall := argIndex[name]
	if !isKeyCall || len(call.Args) <= index {
		return keyCall{}, false
	}
	key, literal := literalKey(call.Args[index], consts)
	return keyCall{where: fset.Position(call.Pos()), name: name, key: key, literal: literal}, true
}

func stringConstants(files []*ast.File) map[string]string {
	consts := make(map[string]string)
	for _, file := range files {
		for _, decl := range file.Decls {
			for _, spec := range constSpecs(decl) {
				addStringConstants(consts, spec)
			}
		}
	}
	return consts
}

func constSpecs(decl ast.Decl) []*ast.ValueSpec {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.CONST {
		return nil
	}
	var specs []*ast.ValueSpec
	for _, spec := range gen.Specs {
		if value, ok := spec.(*ast.ValueSpec); ok {
			specs = append(specs, value)
		}
	}
	return specs
}

func addStringConstants(consts map[string]string, spec *ast.ValueSpec) {
	for i, name := range spec.Names {
		if i >= len(spec.Values) {
			return
		}
		if key, ok := stringLiteral(spec.Values[i]); ok {
			consts[name.Name] = key
		}
	}
}

func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

func literalKey(expr ast.Expr, consts map[string]string) (string, bool) {
	if ident, ok := expr.(*ast.Ident); ok {
		key, found := consts[ident.Name]
		return key, found
	}
	return stringLiteral(expr)
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
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

func TestOperatorLanguageComesFromHerrscherLang(t *testing.T) {
	tests := []struct {
		value  string
		want   i18n.Language
		warned bool
	}{
		{"", i18n.English, false},
		{"en", i18n.English, false},
		{"fr", i18n.French, false},
		{"fr_FR.UTF-8", i18n.French, false},
		{"de", i18n.English, true},
	}
	for _, tc := range tests {
		var logs bytes.Buffer
		t.Setenv(operatorLanguageEnv, tc.value)
		msgs, err := messagesFromEnv(slog.New(slog.NewTextHandler(&logs, nil)))
		if err != nil {
			t.Fatal(err)
		}
		if msgs.lang != tc.want {
			t.Errorf("HERRSCHER_LANG=%q: lang %s, want %s", tc.value, msgs.lang, tc.want)
		}
		wantWarnings := 0
		if tc.warned {
			wantWarnings = 1
		}
		if got := strings.Count(logs.String(), operatorLanguageEnv); got != wantWarnings {
			t.Errorf("HERRSCHER_LANG=%q: %d warnings, want %d in %q", tc.value, got, wantWarnings, logs.String())
		}
	}
}

func TestNotifyPostsFallbackAndReturnsRenderError(t *testing.T) {
	f := &fakeClient{}
	g := NewGateway(f, testRun(), testMessages(), testLog())
	notice := contracts.Notice{Code: contracts.Code(9999)}
	_, err := g.Notify(context.Background(), contracts.Conversation{Gateway: gatewayKind, ID: "c1"}, i18n.English, notice)
	if !errors.Is(err, contracts.ErrUnknownCode) {
		t.Fatalf("Notify returned %v, want the render error", err)
	}
	if !errors.Is(err, ErrFallbackPosted) {
		t.Fatalf("Notify returned %v, want ErrFallbackPosted so the host never retries", err)
	}
	if len(f.sent) != 1 || !strings.Contains(f.sent[0].content, "```") {
		t.Fatalf("sent %+v, want one fallback block", f.sent)
	}
}

func TestUndeclaredKeyIsNeverRenderedRaw(t *testing.T) {
	got := testMessages().say("no.such.key", nil)
	if got == "no.such.key" || !strings.HasPrefix(got, "something went wrong") || !strings.Contains(got, "```") {
		t.Errorf("got %q, want the fallback block", got)
	}
}

func TestTextArgumentCannotPingLinkOrFormat(t *testing.T) {
	notice := contracts.Notice{Code: contracts.CodeSessionCreated, Args: []contracts.Arg{contracts.Text("Name", "@everyone `x` [a](https://evil.example) **b**")}}
	got, err := testMessages().notice(i18n.English, notice)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"@everyone", "`x`", "[a](", "**b**"} {
		if strings.Contains(got, raw) {
			t.Errorf("raw %q survives in %q", raw, got)
		}
	}
}

func TestNeutraliseDefusesMentionsAndMarkdown(t *testing.T) {
	tests := map[string]string{
		"@everyone":           "@\u200beveryone",
		"`code`":              "`\u200bcode`\u200b",
		"[masked](https://e)": "\\[masked\\]\\(https://e\\)",
		"**bold** ~~x~~ _y_":  "\\*\\*bold\\*\\* \\~\\~x\\~\\~ \\_y\\_",
		"# heading | spoiler": "\\# heading \\| spoiler",
	}
	for in, want := range tests {
		if got := neutralise(in); got != want {
			t.Errorf("neutralise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBindFailureIsShownAsNeutralisedBlock(t *testing.T) {
	err := contracts.NoticeOf(testError("boom ``` @everyone [a](https://e.example)"))
	got := testMessages().noticeText(i18n.English, err)
	if n := strings.Count(got, "```"); n != 2 {
		t.Errorf("got %d code fences in %q, want one block", n, got)
	}
	if strings.Contains(got, "@everyone") {
		t.Errorf("raw mention survives in %q", got)
	}
}

func TestUnexpectedWithoutDetailHasExplicitFallback(t *testing.T) {
	got, err := testMessages().notice(i18n.English, contracts.Notice{Code: contracts.CodeUnexpected})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "```") || !strings.Contains(got, "the error carries no detail") {
		t.Errorf("got %q, want the explicit no-detail fallback", got)
	}
}

func TestEmptyRefRendersAsUnknownChannel(t *testing.T) {
	notice := contracts.Notice{Code: contracts.CodeSessionCreated, Args: []contracts.Arg{contracts.Ref("Name", "")}}
	got, err := testMessages().notice(i18n.English, notice)
	if err != nil {
		t.Fatal(err)
	}
	if got != "session an unknown channel created" {
		t.Errorf("got %q", got)
	}
}

func TestCapLimitUsesTheCeilingGiven(t *testing.T) {
	if got := capLimit("500", readCap); got != readCap {
		t.Errorf("above the cap: got %d, want %d", got, readCap)
	}
	if got := capLimit("7", readCap); got != 7 {
		t.Errorf("within the cap: got %d, want 7", got)
	}
	if got := capLimit("7", 5); got != 5 {
		t.Errorf("ceiling 5: got %d, want 5", got)
	}
}

func TestFrenchAllowListPutsASpaceBeforeTheColon(t *testing.T) {
	got := testMessages().sayIn(i18n.French, "allow.list", map[string]any{"Label": "x", "Mentions": "y"})
	if got != "x : y" {
		t.Errorf("got %q, want %q", got, "x : y")
	}
}
