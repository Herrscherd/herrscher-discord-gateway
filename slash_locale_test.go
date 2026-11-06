package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

func slashCommandBuilders(lab labelSet) []*dctl.Command {
	return []*dctl.Command{
		commandSet(lab),
		commandSession(lab),
		commandService(lab),
		commandAllow(lab),
		commandStop(lab),
		commandVerbosity(lab),
		commandMode(lab),
	}
}

func catalogTexts(lang i18n.Language) map[string]bool {
	texts := make(map[string]bool)
	for _, text := range newLabels(testMessages()).texts[lang] {
		texts[text] = true
	}
	return texts
}

type localeCheck struct {
	english map[string]bool
	french  map[string]bool
}

func newLocaleCheck() localeCheck {
	return localeCheck{english: catalogTexts(i18n.English), french: catalogTexts(i18n.French)}
}

func TestSlashCommandsCarryFrenchLocalizations(t *testing.T) {
	check := newLocaleCheck()
	for _, cmd := range slashCommandBuilders(newLabels(testMessages())) {
		raw, err := json.Marshal(cmd.JSON())
		if err != nil {
			t.Fatal(err)
		}
		var tree any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatal(err)
		}
		checkLocalized(t, tree, "/"+fmt.Sprint(cmd.JSON()["name"]), check)
	}
}

func checkLocalized(t *testing.T, node any, where string, check localeCheck) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		if _, ok := v["description"]; ok {
			requireFrench(t, v, "description", "description_localizations", where, check)
		}
		if _, ok := v["value"]; ok && v["name"] != v["value"] {
			requireFrench(t, v, "name", "name_localizations", where, check)
		}
		for key, child := range v {
			checkLocalized(t, child, where+"."+key, check)
		}
	case []any:
		for i, child := range v {
			checkLocalized(t, child, fmt.Sprintf("%s[%d]", where, i), check)
		}
	}
}

func requireFrench(t *testing.T, node map[string]any, field, localized, where string, check localeCheck) {
	t.Helper()
	base, isBase := node[field].(string)
	if !isBase || !check.english[base] {
		t.Errorf("%s: %s is not the English catalogue text", where, field)
	}
	loc, isMap := node[localized].(map[string]any)
	french, isFrench := loc["fr"].(string)
	if !isMap || !isFrench {
		t.Errorf("%s: %s has no French text", where, localized)
		return
	}
	if !check.french[french] || check.english[french] {
		t.Errorf("%s: %s = %q, want the French catalogue text", where, localized, french)
	}
}

func TestStopCarriesTheFrenchDescription(t *testing.T) {
	raw, err := json.Marshal(commandStop(newLabels(testMessages())).JSON())
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Description string            `json:"description"`
		Localized   map[string]string `json:"description_localizations"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Description != "cancel the current turn in this conversation" {
		t.Fatalf("english description = %q", got.Description)
	}
	if want := "annuler le tour en cours dans cette conversation"; got.Localized["fr"] != want {
		t.Fatalf("french description = %q, want %q", got.Localized["fr"], want)
	}
}

func TestReplyLanguageFollowsTheInteractionOutsideSessionChannels(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	s := &slash{log: testLog(), msgs: testMessages(), router: r, ctrl: ctrl}
	cases := []struct {
		name   string
		locale dctl.Locale
		want   i18n.Language
	}{
		{"french interaction", dctl.LocaleFR, i18n.French},
		{"english interaction", "en-US", i18n.English},
		{"unsupported locale falls back to the operator default", "de", i18n.English},
		{"empty locale falls back to the operator default", "", i18n.English},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.replyLanguage(dctl.Interaction{ChannelID: "c9", Locale: tc.locale})
			if got != tc.want {
				t.Fatalf("replyLanguage = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReplyLanguageFallsBackToTheOperatorDefaultWhenLocaleIsMissing(t *testing.T) {
	french, err := newMessages(i18n.French, testLog())
	if err != nil {
		t.Fatal(err)
	}
	r, ctrl, _ := newTestRouter(t)
	s := &slash{log: testLog(), msgs: french, router: r, ctrl: ctrl}
	if got := s.replyLanguage(dctl.Interaction{ChannelID: "c9", Locale: ""}); got != i18n.French {
		t.Fatalf("replyLanguage = %v, want the operator default French", got)
	}
	if got := s.replyLanguage(dctl.Interaction{ChannelID: "c9", Locale: "es-ES"}); got != i18n.French {
		t.Fatalf("replyLanguage(es-ES) = %v, want the operator default French", got)
	}
}

func TestSessionChannelReplyFollowsTheInteractionLocale(t *testing.T) {
	r, ctrl, _ := boundRouter(t)
	ctrl.sessions = []contracts.SessionInfo{{Name: "ch-c1", ChannelID: "c1", Language: i18n.English}}
	s := &slash{log: testLog(), msgs: testMessages(), router: r, ctrl: ctrl}
	got := s.replyLanguage(dctl.Interaction{ChannelID: "c1", Locale: dctl.LocaleFR})
	if got != i18n.French {
		t.Fatalf("replyLanguage in a session channel = %v, want the interaction locale French", got)
	}
}

func TestNoRouterMeansTheInteractionLocaleDecides(t *testing.T) {
	s := &slash{log: testLog(), msgs: testMessages()}
	if got := s.replyLanguage(dctl.Interaction{ChannelID: "c1", Locale: dctl.LocaleFR}); got != i18n.French {
		t.Fatalf("replyLanguage without a router = %v, want French", got)
	}
}

func TestRenderedVerbosityReplyFollowsItsLanguage(t *testing.T) {
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog())
	s := &slash{log: testLog(), msgs: testMessages(), binds: binds}

	french := testMessages().in(i18n.French)
	if got, want := s.setVerbosity(french, "c1", levelFull), "verbosité de ce salon : "+levelFull; got != want {
		t.Fatalf("french verbosity reply = %q, want %q", got, want)
	}
	if got, want := s.setVerbosity(testMessages(), "c2", levelFull), "verbosity of this channel: "+levelFull; got != want {
		t.Fatalf("english verbosity reply = %q, want %q", got, want)
	}
}

func TestRenderedStopReplyIsInFrench(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	s := &slash{log: testLog(), msgs: testMessages(), router: r, ctrl: ctrl}
	if got, want := s.stop(testMessages().in(i18n.French), "c9", "", true), "aucun tour en cours ici"; got != want {
		t.Fatalf("french stop reply = %q, want %q", got, want)
	}
}

type recordedCall struct {
	method string
	path   string
	body   map[string]any
}

type recordingTransport struct {
	calls []recordedCall
}

func (tr *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				return nil, err
			}
		}
	}
	tr.calls = append(tr.calls, recordedCall{method: req.Method, path: req.URL.Path, body: body})
	payload := `{}`
	if strings.HasSuffix(req.URL.Path, "/users/@me") {
		payload = `{"id":"42"}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(payload)),
		Request:    req,
	}, nil
}

func (tr *recordingTransport) replies() []string {
	var out []string
	for _, call := range tr.calls {
		if content, ok := call.body["content"].(string); ok {
			out = append(out, content)
			continue
		}
		if data, ok := call.body["data"].(map[string]any); ok {
			if content, ok := data["content"].(string); ok {
				out = append(out, content)
			}
		}
	}
	return out
}

func interactionSlash(t *testing.T, tr *recordingTransport) *slash {
	t.Helper()
	client := dctl.New("t", "chan",
		dctl.WithHTTPClient(&http.Client{Transport: tr}),
		dctl.WithGlobalCommands(),
	)
	allow := newAllowStore(filepath.Join(t.TempDir(), "allow.json"), testLog())
	if err := allow.AddGlobal("u1"); err != nil {
		t.Fatal(err)
	}
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog())
	return newSlash(client.Interactions(), nil, noGatewaySocket, "t", allow, binds, testMessages(), testLog())
}

func frenchInteraction(options ...dctl.InteractionOption) dctl.Interaction {
	return dctl.Interaction{
		ID:        "i1",
		Token:     "tok",
		ChannelID: "c9",
		Locale:    "fr",
		Member:    dctl.Member{User: dctl.Author{ID: "u1"}},
		Data:      dctl.InteractionData{Options: options},
	}
}

func frenchReply(t *testing.T, key string) string {
	t.Helper()
	english := testMessages().sayIn(i18n.English, key, nil)
	french := testMessages().sayIn(i18n.French, key, nil)
	if french == english {
		t.Fatalf("%s has the same French and English text %q", key, french)
	}
	return french
}

func requireReplies(t *testing.T, tr *recordingTransport, want ...string) {
	t.Helper()
	if got := tr.replies(); !slices.Equal(got, want) {
		t.Fatalf("replies = %q, want %q", got, want)
	}
}

func TestFrenchModeInteractionGetsAFrenchEphemeralReply(t *testing.T) {
	tr := &recordingTransport{}
	s := interactionSlash(t, tr)
	ix := frenchInteraction(dctl.InteractionOption{Name: "mode", Type: 3, Value: "off"})
	if _, err := s.handleMode(context.Background(), ix); err != nil {
		t.Fatal(err)
	}
	requireReplies(t, tr, frenchReply(t, "discord.mode.off"))
}

func TestFrenchStopInteractionGetsAFrenchEphemeralReply(t *testing.T) {
	tr := &recordingTransport{}
	s := interactionSlash(t, tr)
	r, ctrl, _ := newTestRouter(t)
	s.router, s.ctrl = r, ctrl
	if _, err := s.handleStop(context.Background(), frenchInteraction()); err != nil {
		t.Fatal(err)
	}
	requireReplies(t, tr, frenchReply(t, "discord.stop.none"))
}

func TestFrenchInteractionDispatchEditsTheResponseInFrench(t *testing.T) {
	tr := &recordingTransport{}
	s := interactionSlash(t, tr)
	_, ctrl, _ := newTestRouter(t)
	s.ctrl = ctrl
	s.dispatch(context.Background(), frenchInteraction(), []string{"status"})
	requireReplies(t, tr, frenchReply(t, "discord.run.done"))
}
