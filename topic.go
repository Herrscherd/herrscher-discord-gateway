package discord

import (
	"strings"
	"unicode"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func isThreadWord(tok string) bool {
	switch tok {
	case "thread", "fil", "privé", "prive":
		return true
	}
	return false
}

func wantsThread(text string) bool {
	for tok := range tokenSet(text) {
		if isThreadWord(tok) {
			return true
		}
	}
	return false
}

const minRepoToken = 4

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
			if repo.Local {
				hit = repo
			}
		default:
			return contracts.RepoRef{}, false
		}
	}
	return hit, found
}

func names(toks map[string]bool, name string) bool {
	if name == "" {
		return false
	}
	if strings.ContainsRune(name, '/') && toks[strings.ToLower(name)] {
		return true
	}
	base := repoBase(name)
	return len([]rune(base)) >= minRepoToken && toks[base]
}

func repoBase(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

func tokenSet(text string) map[string]bool {
	out := make(map[string]bool)
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

const threadNameMax = 100

func outsideAngles(text string) string {
	var kept []rune
	depth := 0
	for _, r := range text {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			kept = append(kept, r)
		}
	}
	return string(kept)
}

func threadName(text string) string {
	name := strings.Join(strings.Fields(outsideAngles(text)), " ")
	if r := []rune(name); len(r) > threadNameMax {
		name = strings.TrimSpace(string(r[:threadNameMax-1])) + "…"
	}
	if name == "" {
		return "session"
	}
	return name
}
