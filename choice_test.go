package discord

import "testing"

func TestChoiceCustomIDRoundTrip(t *testing.T) {
	id := ChoiceCustomID("mysession")
	name, ok := ParseChoiceCustomID(id)
	if !ok || name != "mysession" {
		t.Fatalf("round trip failed: id=%q name=%q ok=%v", id, name, ok)
	}
	if _, ok := ParseChoiceCustomID("not-a-choice"); ok {
		t.Fatalf("non-choice custom_id must report ok=false")
	}
}

func TestBindCustomIDRoundTrips(t *testing.T) {
	id := BindCustomID("chan1")
	got, ok := ParseBindCustomID(id)
	if !ok || got != "chan1" {
		t.Fatalf("ParseBindCustomID(%q) = %q,%v", id, got, ok)
	}
	if _, ok := ParseBindCustomID(ChoiceCustomID("sess1")); ok {
		t.Fatal("a choice id must not parse as a bind id — the two routes would cross")
	}
	if _, ok := ParseChoiceCustomID(BindCustomID("chan1")); ok {
		t.Fatal("a bind id must not parse as a choice id")
	}
}
