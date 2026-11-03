package discord

import "strings"

type idSet map[string]bool

func newIDSet(csv string) idSet {
	set := make(idSet)
	for _, raw := range strings.Split(csv, ",") {
		entry := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		if entry != "" {
			set[entry] = true
		}
	}
	return set
}

func (s idSet) empty() bool { return len(s) == 0 }

func (s idSet) wildcard() bool { return s["*"] }

func (s idSet) has(identifier string) bool {
	if identifier == "" {
		return false
	}
	return s[identifier]
}

func (s idSet) matches(ids ...string) bool {
	if s.wildcard() {
		return true
	}
	for _, id := range ids {
		if s.has(id) {
			return true
		}
	}
	return false
}
