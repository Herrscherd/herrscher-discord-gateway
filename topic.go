package discord

import (
	"strings"
	"unicode"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

// This file reads intent out of the operator's own words, so the bot asks fewer
// questions. Every rule here is deliberately conservative: guessing wrong costs
// a channel bound to the wrong repo, or a job done in public that was meant to
// be private, and both are worse than one extra click.

// threadWords are the words that ask for the job to happen in a private thread.
// "fil" is the French word the operator actually uses; "privé" covers asking for
// privacy without naming the mechanism.
var threadWords = map[string]bool{
	"thread": true,
	"fil":    true,
	"privé":  true,
	"prive":  true,
}

// wantsThread reports whether the message asks for its own private thread.
func wantsThread(text string) bool {
	for tok := range tokenSet(text) {
		if threadWords[tok] {
			return true
		}
	}
	return false
}

// minRepoToken is the shortest bare repo name matched on its own. Below it a
// name is likely a common word ("web", "api", "doc") that would bind a channel
// to a repo the message never meant to name.
const minRepoToken = 4

// matchRepo finds the one repo the message names, so the menu can be skipped —
// the operator usually says which repo in the ping itself, and being asked
// anyway is pure friction. It reports false unless exactly one repo matches:
// naming two repos, or none, is not an answer, and the binding it would produce
// outlives the message.
func matchRepo(text string, repos []contracts.RepoRef) (contracts.RepoRef, bool) {
	toks := tokenSet(text)
	var hit contracts.RepoRef
	found := false
	for _, repo := range repos {
		if !names(toks, repo.Name) {
			continue
		}
		switch {
		case !found:
			hit, found = repo, true
		case repoBase(hit.Name) == repoBase(repo.Name):
			// The same repo offered twice — a local checkout and its remote — is
			// one answer, not an ambiguity. Prefer the checkout: there is nothing
			// to clone.
			if repo.Local {
				hit = repo
			}
		default:
			return contracts.RepoRef{}, false
		}
	}
	return hit, found
}

// names reports whether the message names this repo, by its full name
// ("owner/repo") or by its bare name.
func names(toks map[string]bool, name string) bool {
	if name == "" {
		return false
	}
	// Spelled with its owner, a name is deliberate whatever its length; bare, it
	// has to clear the floor.
	if strings.ContainsRune(name, '/') && toks[strings.ToLower(name)] {
		return true
	}
	base := repoBase(name)
	return len([]rune(base)) >= minRepoToken && toks[base]
}

// repoBase is the repo name without its owner, lowercased.
func repoBase(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

// tokenSet lowercases the message and splits it into words. '-', '_', '.' and
// '/' stay inside a word so "herrscher-discord-gateway" is one token and cannot
// also match a repo called "herrscher" — a substring search would call that
// ambiguous and fall back to the menu for every ping.
func tokenSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, tok := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		switch r {
		case '-', '_', '.', '/':
			return false
		}
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out[tok] = true
	}
	return out
}

// threadNameMax is Discord's cap on a thread name.
const threadNameMax = 100

// threadName titles the thread after what was asked, so a sidebar full of them
// stays readable. Mentions are stripped: "<@1234>" is noise to a human.
func threadName(text string) string {
	var b strings.Builder
	depth := 0
	for _, r := range text {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	name := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(name); len(r) > threadNameMax {
		name = strings.TrimSpace(string(r[:threadNameMax-1])) + "…"
	}
	if name == "" {
		return "session"
	}
	return name
}
