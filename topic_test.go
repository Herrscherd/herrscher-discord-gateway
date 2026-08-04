package discord

import (
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestMatchRepoReadsTheRepoOutOfThePing(t *testing.T) {
	repos := []contracts.RepoRef{
		{Name: "herrscher", Local: true},
		{Name: "herrscher-discord-gateway", Local: true},
		{Name: "Herrscherd/dctl"},
		{Name: "web"},
	}
	cases := []struct {
		name, text, want string
	}{
		{"bare name", "regarde le bug de auth dans herrscher stp", "herrscher"},
		{"case insensitive", "dans Herrscher il y a un souci", "herrscher"},
		{"punctuation around it", "bug sur herrscher, urgent.", "herrscher"},
		// A hyphenated name is one word: it must not also read as the shorter
		// repo it starts with, or every ping would look ambiguous.
		{"longest name wins on its own", "PR sur herrscher-discord-gateway", "herrscher-discord-gateway"},
		{"full owner/repo", "un fix dans Herrscherd/dctl", "Herrscherd/dctl"},
		{"bare name of an owner/repo", "un fix dans dctl", "Herrscherd/dctl"},
		{"nothing named", "corrige le bug de connexion", ""},
		{"two repos named", "aligne herrscher et dctl", ""},
		// Below the length floor a repo name is a word people write by accident.
		{"too short to be a name", "le web est cassé", ""},
		{"a substring is not a mention", "herrschers", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := matchRepo(c.text, repos)
			if c.want == "" {
				if ok {
					t.Fatalf("matchRepo(%q) = %q, want the menu", c.text, got.Name)
				}
				return
			}
			if !ok || got.Name != c.want {
				t.Fatalf("matchRepo(%q) = %q/%v, want %q", c.text, got.Name, ok, c.want)
			}
		})
	}
}

// The same repo listed as a checkout and as a remote is one answer. Preferring
// the checkout skips a clone of something already on disk.
func TestMatchRepoPrefersTheLocalCheckout(t *testing.T) {
	repos := []contracts.RepoRef{{Name: "Herrscherd/dctl"}, {Name: "dctl", Local: true}}
	got, ok := matchRepo("un fix dans dctl", repos)
	if !ok || !got.Local {
		t.Fatalf("matchRepo = %+v/%v, want the local checkout", got, ok)
	}
}

func TestWantsThread(t *testing.T) {
	yes := []string{
		"ouvre un thread pour ça",
		"fais ça dans un fil stp",
		"règle ça en privé",
		"en prive et tu me dis",
		"Thread please",
	}
	no := []string{
		"corrige le bug de connexion",
		"regarde le profil de l'utilisateur", // "profil" is not "fil"
		"le fichier threads.go est à revoir", // "threads.go" is one token
		"privée sur la page",                 // not the standalone word
	}
	for _, s := range yes {
		if !wantsThread(s) {
			t.Errorf("wantsThread(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if wantsThread(s) {
			t.Errorf("wantsThread(%q) = true, want false", s)
		}
	}
}

func TestThreadName(t *testing.T) {
	if got := threadName("<@123> répare le  webhook   stp"); got != "répare le webhook stp" {
		t.Fatalf("threadName = %q, want the mention stripped and spaces collapsed", got)
	}
	if got := threadName("<@123>"); got != "session" {
		t.Fatalf("threadName = %q, want a fallback rather than an empty name", got)
	}
	long := ""
	for range 200 {
		long += "é"
	}
	got := []rune(threadName(long))
	if len(got) > threadNameMax {
		t.Fatalf("threadName length = %d runes, want <= %d (Discord rejects longer)", len(got), threadNameMax)
	}
}
