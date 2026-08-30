package discord

import "testing"

func TestAChannelGivesEveryoneTheirOwnSession(t *testing.T) {
	s := defaultScope()
	if got := s.slot(chatChannel, "c1", "u1"); got != "c1:u1" {
		t.Fatalf("slot = %q, want c1:u1", got)
	}
	if s.slot(chatChannel, "c1", "u1") == s.slot(chatChannel, "c1", "u2") {
		t.Fatal("two people in one channel shared a session")
	}
	if s.multiUser(chatChannel) {
		t.Fatal("a per-user channel session was marked multi-user")
	}
}

func TestAThreadIsOnePieceOfWorkEveryoneShares(t *testing.T) {
	s := defaultScope()
	if got := s.slot(chatThread, "t1", "u1"); got != "t1" {
		t.Fatalf("slot = %q, want t1", got)
	}
	if !s.multiUser(chatThread) {
		t.Fatal("a shared thread was not marked multi-user")
	}
}

func TestADMIsNeverSharedAndNeverSuffixed(t *testing.T) {
	s := defaultScope()
	if got := s.slot(chatDM, "d1", "u1"); got != "d1" {
		t.Fatalf("slot = %q, want d1: a DM channel already belongs to one person", got)
	}
	if s.multiUser(chatDM) {
		t.Fatal("a DM was marked multi-user")
	}
}

func TestTheTwoFlagsFlipBothRules(t *testing.T) {
	s := sessionScope{groupPerUser: false, threadPerUser: true}
	if got := s.slot(chatChannel, "c1", "u1"); got != "c1" {
		t.Fatalf("slot = %q, want c1 with sessions_per_user off", got)
	}
	if !s.multiUser(chatChannel) {
		t.Fatal("a shared channel was not marked multi-user")
	}
	if got := s.slot(chatThread, "t1", "u1"); got != "t1:u1" {
		t.Fatalf("slot = %q, want t1:u1 with thread_sessions_per_user on", got)
	}
	if s.multiUser(chatThread) {
		t.Fatal("a per-user thread session was marked multi-user")
	}
}

func TestASlotNamesASessionAndKeepsItsConversation(t *testing.T) {
	if got := sessionNameFor("c1"); got != "ch-c1" {
		t.Fatalf("sessionNameFor = %q, want ch-c1", got)
	}
	if got := sessionNameFor("c1:u1"); got != "ch-c1-u1" {
		t.Fatalf("sessionNameFor = %q, want ch-c1-u1", got)
	}
	if got := convOfSlot("c1:u1"); got != "c1" {
		t.Fatalf("convOfSlot = %q, want c1", got)
	}
	if got := convOfSlot("c1"); got != "c1" {
		t.Fatalf("convOfSlot = %q, want c1", got)
	}
}
