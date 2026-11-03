package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Herrscherd/dctl"
	"github.com/coder/websocket"
)

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

func TestDispatchRoutesMessageCreate(t *testing.T) {
	msgs := make(chan messageCreate, 1)
	ixs := make(chan dctl.Interaction, 1)
	w := newWS(gatewayURL, "t", testLog(),
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

func TestDispatchIgnoresUnknownEvents(t *testing.T) {
	called := make(chan struct{}, 2)
	w := newWS(gatewayURL, "t", testLog(),
		func(context.Context, dctl.Interaction) { called <- struct{}{} },
		func(context.Context, messageCreate) { called <- struct{}{} },
		func(context.Context, string) { called <- struct{}{} },
	)
	w.dispatch(context.Background(), "TYPING_START", json.RawMessage(`{"channel_id":"c1"}`))
	w.dispatch(context.Background(), "MESSAGE_CREATE", json.RawMessage(`not json`))
	w.dispatch(context.Background(), "CHANNEL_DELETE", json.RawMessage(`{}`))
	select {
	case <-called:
		t.Fatal("an unhandled event reached a handler")
	default:
	}
}

func TestDispatchRoutesDeletes(t *testing.T) {
	for _, event := range []string{"CHANNEL_DELETE", "THREAD_DELETE"} {
		gone := make(chan string, 1)
		w := newWS(gatewayURL, "t", testLog(), nil, nil, func(_ context.Context, id string) { gone <- id })
		w.dispatch(context.Background(), event, json.RawMessage(`{"id":"c1","guild_id":"g1"}`))
		if id := <-gone; id != "c1" {
			t.Fatalf("%s delivered id %q, want c1", event, id)
		}
	}
}

func TestIntentsIncludeGuilds(t *testing.T) {
	if wsIntents&1 == 0 {
		t.Fatal("wsIntents lacks GUILDS: channel and thread deletes will never be delivered")
	}
}

func TestBoundedRespectsCancelledContext(t *testing.T) {
	w := newWS(gatewayURL, "t", testLog(), nil, nil, nil)
	for range maxInFlight {
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

const lifetimeRounds = 50

func fakeGateway(t *testing.T, script func(context.Context, *websocket.Conn)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(rw, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		script(r.Context(), conn)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func serveOneInteraction(ctx context.Context, conn *websocket.Conn) {
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"op":10,"d":{"heartbeat_interval":3600000}}`)); err != nil {
		return
	}
	if _, _, err := conn.Read(ctx); err != nil {
		return
	}
	for _, frame := range []string{
		`{"op":0,"s":1,"t":"READY","d":{}}`,
		`{"op":0,"s":2,"t":"INTERACTION_CREATE","d":{}}`,
	} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
			return
		}
	}
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}

func TestRunReturnsOnlyAfterInFlightHandlersFinish(t *testing.T) {
	for range lifetimeRounds {
		started, finished := make(chan struct{}), make(chan struct{})
		handle := func(ctx context.Context, _ dctl.Interaction) {
			close(started)
			<-ctx.Done()
			runtime.Gosched()
			close(finished)
		}
		w := newWS(fakeGateway(t, serveOneInteraction), "t", testLog(), handle, nil, nil)
		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan struct{})
		go func() {
			defer close(runDone)
			w.run(ctx)
		}()
		<-started
		cancel()
		<-runDone
		select {
		case <-finished:
		default:
			t.Fatal("run returned while an interaction handler was still running")
		}
	}
}

func TestStartReturnsOnlyAfterCommandSyncFinishes(t *testing.T) {
	for range lifetimeRounds {
		entered, finished := make(chan struct{}), make(chan struct{})
		client := dctl.New("t", "chan",
			dctl.WithHTTPClient(&http.Client{Transport: blockingTransport{entered: entered, finished: finished}}),
			dctl.WithGlobalCommands(),
		)
		s := newSlash(client.Interactions(), nil, fakeGateway(t, serveOneInteraction), "t", nil, nil, testLog())
		ctx, cancel := context.WithCancel(context.Background())
		startDone := make(chan struct{})
		go func() {
			defer close(startDone)
			s.start(ctx)
		}()
		<-entered
		cancel()
		<-startDone
		select {
		case <-finished:
		default:
			t.Fatal("start returned while the command sync was still running")
		}
	}
}

type blockingTransport struct {
	entered  chan struct{}
	finished chan struct{}
}

func (b blockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	close(b.entered)
	<-req.Context().Done()
	close(b.finished)
	return nil, req.Context().Err()
}

func TestStopWaitsForHeartbeatLoopToExit(t *testing.T) {
	for range lifetimeRounds {
		w := newWS(gatewayURL, "t", testLog(), nil, nil, nil)
		var stopped atomic.Bool
		ticked := make(chan struct{}, 1)
		beat := func(context.Context) error {
			if stopped.Load() {
				t.Error("the heartbeat ran after stop returned")
			}
			select {
			case ticked <- struct{}{}:
			default:
			}
			return nil
		}
		stop := w.startHeartbeat(context.Background(), time.Nanosecond, beat)
		<-ticked
		stop()
		stopped.Store(true)
	}
}
