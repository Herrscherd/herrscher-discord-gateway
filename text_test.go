package discord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
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

const keyVar = "key"

type keyCall struct {
	where   token.Position
	name    string
	key     string
	literal bool
	misuse  bool
}

const (
	gatewayPkg   = "github.com/Herrscherd/herrscher-discord-gateway"
	contractsPkg = "github.com/Herrscherd/herrscher-contracts"
	catalogPkg   = contractsPkg + "/i18n"
)

func TestEveryKeyTheCodeUsesIsDeclared(t *testing.T) {
	fset := token.NewFileSet()
	files := productionFiles(t, fset)
	info := productionTypes(t, fset, files)
	used := make(map[string]bool)
	for _, file := range files {
		for _, decl := range file.Decls {
			for _, call := range keyFindings(fset, info, decl) {
				checkKeyCall(t, call, used)
			}
		}
	}
	for key := range declaredMessages() {
		if !used[key] {
			t.Errorf("key %q is declared but no code uses it", key)
		}
	}
}

func checkKeyCall(t *testing.T, call keyCall, used map[string]bool) {
	t.Helper()
	if call.misuse {
		t.Errorf("%s: the key parameter is used in a form the analysis cannot follow", call.where)
		return
	}
	if !call.literal {
		t.Errorf("%s: the key of %s is neither a string literal nor a package constant", call.where, call.name)
		return
	}
	used[call.key] = true
	if _, declared := declaredMessages()[call.key]; !declared {
		t.Errorf("%s: key %q is used by the code but not declared", call.where, call.key)
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

func productionTypes(t *testing.T, fset *token.FileSet, files []*ast.File) *types.Info {
	t.Helper()
	info := &types.Info{Defs: make(map[*ast.Ident]types.Object), Uses: make(map[*ast.Ident]types.Object)}
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "source", nil),
		Error:    func(err error) { t.Error(err) },
	}
	if _, err := conf.Check(gatewayPkg, fset, files, info); err != nil {
		t.Fatal(err)
	}
	return info
}

func keyFindings(fset *token.FileSet, info *types.Info, decl ast.Decl) []keyCall {
	param := funnelKeyParam(decl, info)
	allowed := make(map[*ast.Ident]bool)
	var findings []keyCall
	ast.Inspect(decl, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		index, isKeyCall := relayKeyIndex(call, info)
		if !isKeyCall || len(call.Args) <= index {
			return true
		}
		arg := call.Args[index]
		if ident, isIdent := arg.(*ast.Ident); isIdent && param != nil && info.Uses[ident] == param {
			allowed[ident] = true
			return true
		}
		findings = append(findings, keyCallOfArg(fset, call, arg, info))
		return true
	})
	if param != nil {
		findings = append(findings, unexplainedKeyUses(fset, decl, info, param, allowed)...)
	}
	return findings
}

func funnelKeyParam(decl ast.Decl, info *types.Info) types.Object {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok {
		return nil
	}
	method, ok := info.Defs[fn.Name].(*types.Func)
	if !ok || !isFunnel(method) {
		return nil
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == keyVar {
				return info.Defs[name]
			}
		}
	}
	return nil
}

func unexplainedKeyUses(fset *token.FileSet, decl ast.Decl, info *types.Info, param types.Object, allowed map[*ast.Ident]bool) []keyCall {
	var misuses []keyCall
	ast.Inspect(decl, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if ok && ident.Name == keyVar && !allowed[ident] && info.Defs[ident] != param {
			misuses = append(misuses, keyCall{where: fset.Position(ident.Pos()), name: keyVar, misuse: true})
		}
		return true
	})
	return misuses
}

func relayKeyIndex(call *ast.CallExpr, info *types.Info) (int, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	method, ok := info.Uses[selector.Sel].(*types.Func)
	if !ok {
		return 0, false
	}
	switch qualifiedMethod(method) {
	case gatewayPkg + ".messages.say", contractsPkg + ".Builder.Help", catalogPkg + ".Catalog.Has":
		return 0, true
	case gatewayPkg + ".messages.sayIn", gatewayPkg + ".messages.catalogText", gatewayPkg + ".messages.failed",
		contractsPkg + ".Builder.Param", contractsPkg + ".Builder.ValueParam", catalogPkg + ".Catalog.Message":
		return 1, true
	}
	return 0, false
}

func isFunnel(method *types.Func) bool {
	switch qualifiedMethod(method) {
	case gatewayPkg + ".messages.say", gatewayPkg + ".messages.sayIn", gatewayPkg + ".messages.catalogText":
		return true
	}
	return false
}

func qualifiedMethod(method *types.Func) string {
	sig, ok := method.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return ""
	}
	recv := sig.Recv().Type()
	if ptr, isPtr := recv.(*types.Pointer); isPtr {
		recv = ptr.Elem()
	}
	named, ok := recv.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	return named.Obj().Pkg().Path() + "." + named.Obj().Name() + "." + method.Name()
}

func keyCallOfArg(fset *token.FileSet, call *ast.CallExpr, arg ast.Expr, info *types.Info) keyCall {
	key, literal := constantKey(arg, info)
	return keyCall{where: fset.Position(call.Pos()), name: calleeName(call), key: key, literal: literal}
}

func constantKey(expr ast.Expr, info *types.Info) (string, bool) {
	ident, isIdent := expr.(*ast.Ident)
	if !isIdent {
		return stringLiteral(expr)
	}
	c, ok := info.Uses[ident].(*types.Const)
	if !ok || c.Pkg() == nil || c.Parent() != c.Pkg().Scope() || c.Val().Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(c.Val()), true
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

func TestRefArgumentIsRefusedForATextParameter(t *testing.T) {
	msgs := testMessages()
	notice := contracts.Notice{Code: contracts.CodeSessionCreated, Args: []contracts.Arg{contracts.Ref("Name", "123")}}
	if got, err := msgs.notice(i18n.English, notice); !errors.Is(err, contracts.ErrNoticeArgs) {
		t.Fatalf("notice returned %q, %v, want ErrNoticeArgs: a ref is not a text", got, err)
	}
	f := &fakeClient{}
	g := NewGateway(f, testRun(), msgs, testLog())
	_, err := g.Notify(context.Background(), contracts.Conversation{Gateway: gatewayKind, ID: "c1"}, i18n.English, notice)
	if !errors.Is(err, contracts.ErrNoticeFallbackPosted) || !errors.Is(err, contracts.ErrNoticeArgs) {
		t.Fatalf("Notify returned %v, want ErrNoticeFallbackPosted wrapping ErrNoticeArgs", err)
	}
	if len(f.sent) != 1 || !strings.HasPrefix(f.sent[0].content, failEmoji+" ") {
		t.Fatalf("sent %+v, want the fallback under the failure emoji", f.sent)
	}
}

func TestRefArgumentKeepsItsKindAndIsNeutralised(t *testing.T) {
	notice := contracts.Notice{Code: contracts.CodeSessionCreated, Args: []contracts.Arg{contracts.Ref("Name", "@everyone")}}
	plain := plainNotice(notice)
	if len(plain.Args) != 1 || plain.Args[0].Kind() != contracts.ArgRef {
		t.Fatalf("plainNotice kept %+v, want one ArgRef", plain.Args)
	}
	if got := plain.Args[0].Value(); got != "@\u200beveryone" {
		t.Errorf("ref value %q, want the mention neutralised", got)
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
	if !errors.Is(err, contracts.ErrNoticeFallbackPosted) {
		t.Fatalf("Notify returned %v, want ErrNoticeFallbackPosted so the host never retries", err)
	}
	if len(f.sent) != 1 || !strings.Contains(f.sent[0].content, "```") {
		t.Fatalf("sent %+v, want one fallback block", f.sent)
	}
}

func TestNotifyReturnsPostErrorWithoutFallbackSentinel(t *testing.T) {
	postErr := errors.New("discord unreachable")
	f := &fakeClient{sendErr: postErr}
	g := NewGateway(f, testRun(), testMessages(), testLog())
	notice := contracts.Notice{Code: contracts.Code(9999)}
	_, err := g.Notify(context.Background(), contracts.Conversation{Gateway: gatewayKind, ID: "c1"}, i18n.English, notice)
	if !errors.Is(err, postErr) {
		t.Fatalf("Notify returned %v, want the Post error", err)
	}
	if errors.Is(err, contracts.ErrNoticeFallbackPosted) {
		t.Fatalf("Notify returned %v with ErrNoticeFallbackPosted, but no fallback reached the channel", err)
	}
}

func TestNewNoticeCodesRenderInFrenchAndEnglish(t *testing.T) {
	tests := []struct {
		name    string
		notice  contracts.Notice
		french  string
		english string
	}{
		{
			name:    "setting invalid",
			notice:  contracts.Notice{Code: contracts.CodeSettingInvalid, Args: []contracts.Arg{contracts.Text("Setting", "mode"), contracts.Text("Value", "loud")}},
			french:  "valeur invalide loud pour le réglage mode",
			english: "invalid value loud for setting mode",
		},
		{
			name:    "setting missing",
			notice:  contracts.Notice{Code: contracts.CodeSettingMissing, Args: []contracts.Arg{contracts.Text("Settings", "token, guild")}},
			french:  "ces réglages doivent être définis : token, guild",
			english: "these settings must be set: token, guild",
		},
		{
			name:    "node merged",
			notice:  contracts.Notice{Code: contracts.CodeNodeMerged},
			french:  "ce nœud est replié dans un umbrella ; restaure-le avec l'option force pour le détacher",
			english: "this node is folded into an umbrella; restore it with force to detach it",
		},
		{
			name:    "compact unavailable",
			notice:  contracts.Notice{Code: contracts.CodeCompactUnavailable, Args: []contracts.Arg{contracts.Text("Reason", "no model")}},
			french:  "compaction du contexte indisponible : no model",
			english: "context compaction unavailable: no model",
		},
		{
			name:    "compacted",
			notice:  contracts.Notice{Code: contracts.CodeCompacted},
			french:  "contexte compacté",
			english: "context compacted",
		},
		{
			name:    "address plaintext refused",
			notice:  contracts.Notice{Code: contracts.CodeAddressPlaintextRefused, Args: []contracts.Arg{contracts.Text("Address", "10.0.0.2:4000")}},
			french:  "connexion en clair vers 10.0.0.2:4000 refusée : le TLS est requis hors boucle locale",
			english: "plaintext connection to 10.0.0.2:4000 refused: TLS is required outside loopback",
		},
	}
	for _, tc := range tests {
		for _, lang := range []struct {
			name string
			id   i18n.Language
			want string
		}{
			{"fr", i18n.French, tc.french},
			{"en", i18n.English, tc.english},
		} {
			t.Run(tc.name+"/"+lang.name, func(t *testing.T) {
				f := &fakeClient{}
				g := NewGateway(f, testRun(), testMessages(), testLog())
				if _, err := g.Notify(context.Background(), contracts.Conversation{Gateway: gatewayKind, ID: "c1"}, lang.id, tc.notice); err != nil {
					t.Fatal(err)
				}
				want := failEmoji + " " + lang.want
				if len(f.sent) != 1 || f.sent[0].content != want {
					t.Fatalf("sent %+v, want %q", f.sent, want)
				}
			})
		}
	}
}

func TestNewNoticeTextArgumentIsNeutralised(t *testing.T) {
	notice := contracts.Notice{Code: contracts.CodeAddressPlaintextRefused, Args: []contracts.Arg{contracts.Text("Address", "@everyone*x")}}
	got, err := testMessages().notice(i18n.English, notice)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "@everyone") || !strings.Contains(got, `\*x`) {
		t.Errorf("text argument not neutralised in %q", got)
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

func TestEmptyChannelRefRendersAsUnknownChannel(t *testing.T) {
	if got := testMessages().channelRef(i18n.English, ""); got != "an unknown channel" {
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
