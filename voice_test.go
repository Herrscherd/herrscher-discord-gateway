package discord

import (
	"regexp"
	"testing"
)

func TestVoiceAddressRoundTrips(t *testing.T) {
	for _, conv := range []string{"123456789012345678", "123456789012345678@bob", "42@ops_team-1"} {
		addr, err := parseAddress(conv)
		if err != nil {
			t.Fatalf("parseAddress(%q): %v", conv, err)
		}
		if got := addr.String(); got != conv {
			t.Fatalf("String() of %q = %q, want the same address back", conv, got)
		}
	}
}

func TestVoiceAddressRefusesMalformedText(t *testing.T) {
	for _, conv := range []string{"", "@bob", "123@", "123@bad name", "123@a@b", "123@-bob", "123@bob!"} {
		if _, err := parseAddress(conv); err == nil {
			t.Fatalf("parseAddress(%q) accepted a malformed address", conv)
		}
	}
}

func TestVoicedAddressKeepsABareChannelBare(t *testing.T) {
	if got := voiced("123", ""); got != "123" {
		t.Fatalf("voiced without an agent = %q, want the bare channel", got)
	}
	if got := voiced("123", "bob"); got != "123@bob" {
		t.Fatalf("voiced(123, bob) = %q, want 123@bob", got)
	}
}

func TestAgentHeaderEscapesMarkup(t *testing.T) {
	if got := agentHeader("ops_bot"); got != "**ops\\_bot**\n" {
		t.Fatalf("agentHeader(ops_bot) = %q, want the name escaped inside bold", got)
	}
	if got := agentHeader(""); got != "" {
		t.Fatalf("agentHeader of the house bot = %q, want no header", got)
	}
}

func TestSessionNameForAVoicedSlotNamesTheAgent(t *testing.T) {
	if got := sessionNameFor("123@bob:456"); got != "ch-123-bob-456" {
		t.Fatalf("sessionNameFor(voiced slot) = %q, want ch-123-bob-456", got)
	}
}

func TestSessionNameStaysWithinTheAgentNameBound(t *testing.T) {
	longSlot := "123456789012345678@" + "a0123456789012345678901234567890123456789012345678901234567"
	name := sessionNameFor(longSlot)
	if len(name) > 64 || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`).MatchString(name) {
		t.Fatalf("sessionNameFor(long slot) = %q, want a name within the session name rule", name)
	}
	if again := sessionNameFor(longSlot); again != name {
		t.Fatalf("sessionNameFor is not deterministic: %q then %q", name, again)
	}
	other := sessionNameFor(longSlot + "x")
	if other == name {
		t.Fatalf("two long slots share the short name %q", name)
	}
}

func TestMentionOfAnAgentNeedsAWholeWord(t *testing.T) {
	cases := map[string]bool{
		"hey @bob fix it": true,
		"@Bob!":           true,
		"ask @bob":        true,
		"x@bob":           false,
		"@bobby":          false,
		"@bob-2":          false,
		"bob":             false,
	}
	for text, want := range cases {
		if got := newAgentMention("bob").matches(text); got != want {
			t.Fatalf("mention of bob in %q = %v, want %v", text, got, want)
		}
	}
}
