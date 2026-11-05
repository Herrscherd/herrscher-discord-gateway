package discord

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Herrscherd/dctl"
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

func TestSlashCommandsCarryFrenchLocalizations(t *testing.T) {
	for _, cmd := range slashCommandBuilders(newLabels(testMessages())) {
		raw, err := json.Marshal(cmd.JSON())
		if err != nil {
			t.Fatal(err)
		}
		var tree any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatal(err)
		}
		checkLocalized(t, tree, "/"+fmt.Sprint(cmd.JSON()["name"]))
	}
}

func checkLocalized(t *testing.T, node any, where string) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		if _, ok := v["description"]; ok {
			requireFrench(t, v, "description", "description_localizations", where)
		}
		if _, ok := v["value"]; ok && v["name"] != v["value"] {
			requireFrench(t, v, "name", "name_localizations", where)
		}
		for key, child := range v {
			checkLocalized(t, child, where+"."+key)
		}
	case []any:
		for i, child := range v {
			checkLocalized(t, child, fmt.Sprintf("%s[%d]", where, i))
		}
	}
}

func requireFrench(t *testing.T, node map[string]any, field, localized, where string) {
	t.Helper()
	if text, isText := node[field].(string); !isText || text == "" {
		t.Errorf("%s: %s is empty", where, field)
	}
	loc, isMap := node[localized].(map[string]any)
	french, isFrench := loc["fr"].(string)
	if !isMap || !isFrench || french == "" {
		t.Errorf("%s: %s has no French text", where, localized)
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

func TestSessionChannelKeepsTheSessionLanguageWhateverTheLocale(t *testing.T) {
	r, ctrl, _ := boundRouter(t)
	s := &slash{log: testLog(), msgs: testMessages(), router: r, ctrl: ctrl}
	got := s.replyLanguage(dctl.Interaction{ChannelID: "c1", Locale: dctl.LocaleFR})
	if got != i18n.English {
		t.Fatalf("replyLanguage in a session channel = %v, want the session language English", got)
	}
}

func TestNoRouterMeansTheInteractionLocaleDecides(t *testing.T) {
	s := &slash{log: testLog(), msgs: testMessages()}
	if got := s.replyLanguage(dctl.Interaction{ChannelID: "c1", Locale: dctl.LocaleFR}); got != i18n.French {
		t.Fatalf("replyLanguage without a router = %v, want French", got)
	}
}

func TestRenderedRepliesFollowTheInteractionLanguage(t *testing.T) {
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
