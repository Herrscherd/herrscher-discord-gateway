package discord

import (
	"strings"
	"testing"
)

func TestAHashedSessionNameNeverEqualsAPlainOne(t *testing.T) {
	hashed := sessionNameFor("123456789012345678@bob" + strings.Repeat("x", 40) + ":12345678901234567")
	plain := sessionNameFor("123456789012345678@bobxx-8dbbbec6b9b2aaea:12345678901234567")
	if plain != "ch-123456789012345678-12345678901234567-a-bobxx-8dbbbec6b9b2aaea" {
		t.Fatalf("the plain slot named %q, the collision pair is no longer the one under test", plain)
	}
	if hashed == plain {
		t.Fatalf("a hashed long slot and a plain slot both get the session name %q", plain)
	}
}

func TestANonNumericIdNeverSharesANameWithANumericSlot(t *testing.T) {
	pairs := [][2]string{
		{"1-2", "1:2"},
		{"1:2-3", "1-2:3"},
	}
	for _, pair := range pairs {
		if sessionNameFor(pair[0]) == sessionNameFor(pair[1]) {
			t.Fatalf("slots %q and %q both get the session name %q", pair[0], pair[1], sessionNameFor(pair[0]))
		}
	}
}

func TestDistinctSlotsGetDistinctSessionNames(t *testing.T) {
	slots := []string{
		"123",
		"123:456",
		"123@456",
		"123@456:789",
		"123@ana:456",
		"123@456-ana",
		"123@x:456",
		"123@x-456",
		"123@x-a-456",
		"123@x-a-456:789",
		"123@x",
		"123@x-a-y",
	}
	seen := make(map[string]string, len(slots))
	for _, slot := range slots {
		name := sessionNameFor(slot)
		if previous, taken := seen[name]; taken {
			t.Fatalf("slots %q and %q both get the session name %q", previous, slot, name)
		}
		seen[name] = slot
	}
}

func TestAVoicedSlotNamesTheAgentAfterItsUser(t *testing.T) {
	if got := sessionNameFor("123@bob:456"); got != "ch-123-456-a-bob" {
		t.Fatalf("sessionNameFor(voiced slot with a user) = %q, want ch-123-456-a-bob", got)
	}
	if got := sessionNameFor("123@bob"); got != "ch-123-a-bob" {
		t.Fatalf("sessionNameFor(voiced slot) = %q, want ch-123-a-bob", got)
	}
}

func TestLongSlotsDifferingOnlyInTheirTailGetDistinctNames(t *testing.T) {
	base := "123456789012345678@" + "a0123456789012345678901234567890123456789012345678901234567"
	names := make(map[string]bool)
	for _, slot := range []string{base, base + "x", base + "y", base + ":456", base + ":457"} {
		name := sessionNameFor(slot)
		if names[name] {
			t.Fatalf("long slot %q shares the session name %q", slot, name)
		}
		names[name] = true
	}
}
