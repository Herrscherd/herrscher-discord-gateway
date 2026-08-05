package discord

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Herrscherd/dctl"
)

// TestIdentifyRequestsOnlyNonPrivilegedMessageIntents pins the intent bitmask:
// GUILD_MESSAGES and DIRECT_MESSAGES are non-privileged and enough (Discord still
// fills content/attachments for messages that mention the app), while
// MESSAGE_CONTENT (1<<15) is privileged and must never be asked for — requesting
// it on an unapproved app closes the gateway with 4014.
func TestIdentifyRequestsOnlyNonPrivilegedMessageIntents(t *testing.T) {
	if wsIntents&(1<<15) != 0 {
		t.Fatal("MESSAGE_CONTENT (1<<15) is privileged and must not be requested")
	}
	if wsIntents&(1<<9) == 0 {
		t.Fatal("GUILD_MESSAGES (1<<9) is required to receive channel messages")
	}
	if wsIntents&(1<<12) == 0 {
		t.Fatal("DIRECT_MESSAGES (1<<12) is required to receive DMs")
	}
	// The IDENTIFY payload must actually carry them, not the old 0.
	b, err := json.Marshal(identifyMsg{Op: opIdentify, D: identifyData{Intents: wsIntents}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		D struct {
			Intents int `json:"intents"`
		} `json:"d"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.D.Intents != wsIntents {
		t.Fatalf("identify intents = %d, want %d", got.D.Intents, wsIntents)
	}
}

// TestDispatchRoutesMessageCreate proves a MESSAGE_CREATE dispatch is decoded
// into messageCreate — including the two fields dctl.Message lacks (mentions and
// referenced_message) that the trigger filter depends on — and handed to
// onMessage, while INTERACTION_CREATE still reaches the interaction handler.
func TestDispatchRoutesMessageCreate(t *testing.T) {
	msgs := make(chan messageCreate, 1)
	ixs := make(chan dctl.Interaction, 1)
	w := newWS("t",
		func(_ context.Context, ix dctl.Interaction) { ixs <- ix },
		func(_ context.Context, m messageCreate) { msgs <- m },
		nil,
	)

	w.dispatch(context.Background(), "MESSAGE_CREATE", json.RawMessage(`{
		"id": "m1",
		"channel_id": "c1",
		"guild_id": "g1",
		"content": "<@bot> fix this",
		"author": {"id": "u1", "username": "shan"},
		"mentions": [{"id": "bot"}],
		"attachments": [{"url": "https://cdn/x.png", "filename": "x.png"}],
		"referenced_message": {"author": {"id": "bot"}}
	}`))

	m := <-msgs
	if m.ID != "m1" || m.ChannelID != "c1" || m.Author.ID != "u1" {
		t.Fatalf("decoded message = %+v", m)
	}
	if len(m.Mentions) != 1 || m.Mentions[0].ID != "bot" {
		t.Fatalf("mentions = %+v, want the bot mention the trigger filter reads", m.Mentions)
	}
	if m.Referenced == nil || m.Referenced.Author.ID != "bot" {
		t.Fatalf("referenced = %+v, want the replied-to author", m.Referenced)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Filename != "x.png" {
		t.Fatalf("attachments = %+v, want the screenshot carried through", m.Attachments)
	}

	w.dispatch(context.Background(), "INTERACTION_CREATE", json.RawMessage(`{"id":"i1"}`))
	if ix := <-ixs; ix.ID != "i1" {
		t.Fatalf("interaction id = %q, want i1", ix.ID)
	}
}

// TestDispatchIgnoresUnknownEvents proves the two message intents' other events
// (READY, TYPING_START, …) and malformed payloads are dropped without reaching a
// handler and without panicking.
func TestDispatchIgnoresUnknownEvents(t *testing.T) {
	called := make(chan struct{}, 2)
	w := newWS("t",
		func(context.Context, dctl.Interaction) { called <- struct{}{} },
		func(context.Context, messageCreate) { called <- struct{}{} },
		func(context.Context, string) { called <- struct{}{} },
	)
	w.dispatch(context.Background(), "TYPING_START", json.RawMessage(`{"channel_id":"c1"}`))
	w.dispatch(context.Background(), "MESSAGE_CREATE", json.RawMessage(`not json`))
	// A delete with no id names nothing to close; it must not reach the handler,
	// where an empty id would be looked up as a conversation.
	w.dispatch(context.Background(), "CHANNEL_DELETE", json.RawMessage(`{}`))
	select {
	case <-called:
		t.Fatal("an unhandled event reached a handler")
	default:
	}
}

// TestDispatchRoutesDeletes proves both delete dispatches reach onGone carrying
// the id of what is gone. A thread and a channel take the same path on purpose:
// either can be the conversation a session drives.
func TestDispatchRoutesDeletes(t *testing.T) {
	for _, event := range []string{"CHANNEL_DELETE", "THREAD_DELETE"} {
		gone := make(chan string, 1)
		w := newWS("t", nil, nil, func(_ context.Context, id string) { gone <- id })
		w.dispatch(context.Background(), event, json.RawMessage(`{"id":"c1","guild_id":"g1"}`))
		if id := <-gone; id != "c1" {
			t.Fatalf("%s delivered id %q, want c1", event, id)
		}
	}
}

// The gateway only learns a conversation was deleted if it asked for the intent
// that carries CHANNEL_DELETE and THREAD_DELETE. Without GUILDS the dispatch
// never arrives and a deleted room leaves its session running forever, which is
// silent — hence a test on the constant itself.
func TestIntentsIncludeGuilds(t *testing.T) {
	if wsIntents&1 == 0 {
		t.Fatal("wsIntents lacks GUILDS: channel and thread deletes will never be delivered")
	}
}

// TestBoundedRespectsCancelledContext proves a cancelled context stops new work
// from being spawned rather than queueing it behind a dead connection.
func TestBoundedRespectsCancelledContext(t *testing.T) {
	w := newWS("t", nil, nil, nil)
	for i := 0; i < maxInFlight; i++ {
		w.sem <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := make(chan struct{}, 1)
	w.bounded(ctx, func() { ran <- struct{}{} })
	select {
	case <-ran:
		t.Fatal("bounded ran fn despite a saturated semaphore and a cancelled context")
	default:
	}
}
