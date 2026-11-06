package discord

import (
	"context"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

func TestCatalogSourceFeedsAHost(t *testing.T) {
	t.Parallel()
	gateway := &Gateway{}
	catalog, err := i18n.NewCatalog(testLog(), gateway.Catalog())
	if err != nil {
		t.Fatalf("NewCatalog over Catalog(): %v", err)
	}
	got, err := catalog.Message(i18n.French, "discord.help.channel_read", nil)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	const want = "lire une conversation : les messages récents d'un salon, du plus ancien au plus récent"
	if got != want {
		t.Fatalf("Message = %q, want %q", got, want)
	}
}

func TestResultsRenderInEachLanguage(t *testing.T) {
	t.Parallel()
	msgs := testMessages()
	tests := []struct {
		lang   i18n.Language
		key    string
		params map[string]any
		want   string
	}{
		{i18n.English, "discord.result.posted", map[string]any{"ID": "m1"}, "posted m1"},
		{i18n.French, "discord.result.posted", map[string]any{"ID": "m1"}, "message m1 publié"},
		{i18n.English, "discord.result.replied", map[string]any{"ID": "m2"}, "replied m2"},
		{i18n.French, "discord.result.replied", map[string]any{"ID": "m2"}, "réponse m2 envoyée"},
		{i18n.English, "discord.result.reacted", nil, "reacted"},
		{i18n.French, "discord.result.reacted", nil, "réaction ajoutée"},
		{i18n.English, "discord.result.unreacted", nil, "unreacted"},
		{i18n.French, "discord.result.unreacted", nil, "réaction retirée"},
		{i18n.English, "discord.result.edited", nil, "edited"},
		{i18n.French, "discord.result.edited", nil, "message modifié"},
		{i18n.English, "discord.result.deleted", nil, "deleted"},
		{i18n.French, "discord.result.deleted", nil, "message supprimé"},
		{i18n.English, "discord.command.embed", nil, "(embed)"},
		{i18n.French, "discord.command.embed", nil, "(contenu enrichi)"},
	}
	for _, tc := range tests {
		got := msgs.sayIn(tc.lang, tc.key, tc.params)
		if got != tc.want {
			t.Errorf("%s in %v = %q, want %q", tc.key, tc.lang, got, tc.want)
		}
	}
}

func TestWriteResultsSpeakTheGatewayLanguage(t *testing.T) {
	t.Parallel()
	msgs, err := newMessages(i18n.French, testLog())
	if err != nil {
		t.Fatal(err)
	}
	g := NewGateway(&fakeClient{}, testRun(), msgs, testLog())
	ctx := context.Background()
	out, err := runText(ctx, cmdNamed(t, g, "message", "reply"),
		contracts.Input{Args: map[string]string{"id": "c1", "to": "m1", "text": "yo"}})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if out != "réponse m2 envoyée" {
		t.Fatalf("reply result = %q, want %q", out, "réponse m2 envoyée")
	}
	out, err = runText(ctx, cmdNamed(t, g, "message", "react"),
		contracts.Input{Args: map[string]string{"id": "c1", "msg": "m1", "emoji": "👀"}})
	if err != nil {
		t.Fatalf("react: %v", err)
	}
	if out != "réaction ajoutée" {
		t.Fatalf("react result = %q, want %q", out, "réaction ajoutée")
	}
}
