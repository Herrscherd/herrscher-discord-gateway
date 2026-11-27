package discord

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestAgentBotsResolveInParallel(t *testing.T) {
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	releaseFirst := func() { releaseOnce.Do(func() { close(firstRelease) }) }
	defer releaseFirst()

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Header.Get("Authorization") {
		case "Bot first":
			select {
			case <-firstRelease:
				return jsonReply(`{"id":"1"}`)(req)
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		case "Bot second":
			startOnce.Do(func() { close(secondStarted) })
			return jsonReply(`{"id":"2"}`)(req)
		}
		return nil, errors.New("unexpected token")
	})
	g := &Gateway{log: testLog(), voices: newVoiceBots(nil), newBot: botsWith(transport)}
	voices := []contracts.AgentVoice{
		{Agent: "bob", Token: botToken(t, "first")},
		{Agent: "carol", Token: botToken(t, "second")},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolved := make(chan []resolvedAgent, 1)
	go func() { resolved <- g.resolveAgents(ctx, voices) }()

	select {
	case <-secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("carol did not start resolving until bob's application id came back: the agents resolve one after another")
	}
	releaseFirst()
	select {
	case got := <-resolved:
		if len(got) != 2 {
			t.Fatalf("resolved = %d agents, want both bob and carol", len(got))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resolution did not finish once bob's application id came back")
	}
}
