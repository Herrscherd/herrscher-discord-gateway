package discord

import "strings"

type idSet map[string]bool

func newIDSet(csv string) idSet {
	s := idSet{}
	for _, raw := range strings.Split(csv, ",") {
		v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		if v != "" {
			s[v] = true
		}
	}
	return s
}

func (s idSet) empty() bool { return len(s) == 0 }

func (s idSet) wildcard() bool { return s["*"] }

func (s idSet) has(id string) bool {
	if id == "" {
		return false
	}
	return s[id]
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
