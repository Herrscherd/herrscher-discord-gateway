package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/coder/websocket"
)

type tokenAppIDs struct{ known string }

func (tr tokenAppIDs) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") != "Bot "+tr.known {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"401: Unauthorized","code":0}`)),
			Request:    req,
		}, nil
	}
	return jsonReply(`{"id":"42"}`)(req)
}

type blockingAppID struct {
	started chan struct{}
	release <-chan struct{}
	once    *sync.Once
}

func (tr blockingAppID) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.once.Do(func() { close(tr.started) })
	select {
	case <-tr.release:
		return nil, errors.New("offline")
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func botToken(t *testing.T, secret string) contracts.BotToken {
	t.Helper()
	token, err := contracts.NewBotToken(secret)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func botsWith(transport http.RoundTripper) func(string) *dctl.Client {
	return func(token string) *dctl.Client {
		return dctl.New(token, "", dctl.WithHTTPClient(&http.Client{Transport: transport}))
	}
}

func identifyingGateway(t *testing.T, identified chan<- string) string {
	t.Helper()
	return fakeGateway(t, func(ctx context.Context, conn *websocket.Conn) {
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"op":10,"d":{"heartbeat_interval":3600000}}`)); err != nil {
			return
		}
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		select {
		case identified <- identifiedToken(data):
		default:
		}
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	})
}

func identifiedToken(data []byte) string {
	var identify struct {
		D struct {
			Token string `json:"token"`
		} `json:"d"`
	}
	if err := json.Unmarshal(data, &identify); err != nil {
		return ""
	}
	return identify.D.Token
}

func TestAgentWithATokenGetsItsOwnSocketAndAFailedOneIsSkipped(t *testing.T) {
	identified := make(chan string, 1)
	url := identifyingGateway(t, identified)
	g := &Gateway{log: testLog(), voices: newVoiceBots(nil), wsURL: url, newBot: botsWith(tokenAppIDs{known: "good"})}
	voices := []contracts.AgentVoice{
		{Agent: "bob", Token: botToken(t, "good")},
		{Agent: "carol", Token: botToken(t, "bad")},
	}

	sockets := g.agentSockets(context.Background(), voices)

	if len(sockets) != 1 {
		t.Fatalf("sockets = %d, want only bob's: carol's token is refused", len(sockets))
	}
	bob, ok := g.voices.ownOf("bob")
	if !ok || bob.appID != "42" {
		t.Fatalf("bob's own bot = %+v, %v, want the application id 42", bob, ok)
	}
	if _, ok := g.voices.ownOf("carol"); ok {
		t.Fatal("carol got a bot of its own although its token was refused")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sockets[0].run(ctx)
	select {
	case got := <-identified:
		if got != "good" {
			t.Fatalf("identified with %q, want bob's token", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bob's socket never identified")
	}
}

func TestCloseDoesNotWaitForAnAgentBotToIdentify(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	transport := blockingAppID{started: started, release: release, once: &sync.Once{}}
	g := gatewayOver(transport)
	g.log = testLog()
	g.voices = newVoiceBots(nil)
	g.newBot = botsWith(transport)
	ctrl := directoryCtrl{fakeCtrl: newFakeCtrl(), voices: []contracts.AgentVoice{{Agent: "bob", Token: botToken(t, "t")}}}

	bound := make(chan struct{})
	go func() {
		defer close(bound)
		g.BindSessionControl(ctrl)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("bob's bot never asked Discord for its application id")
	}

	closed := make(chan error, 1)
	go func() { closed <- g.Close(context.Background()) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Close waited on Discord for an agent bot to identify")
	}

	close(release)
	<-bound
	if g.slashRunning != nil {
		t.Fatal("the slash runtime started after Close")
	}
}

func TestHouseIdentifiesWithContentOnlyForATokenlessChannelsVoice(t *testing.T) {
	cases := map[string]struct {
		voice contracts.AgentVoice
		want  bool
	}{
		"tokenless channels":  {contracts.AgentVoice{Agent: "bob", Reply: contracts.ReplyInChannels, Channels: []string{"c1"}}, true},
		"channels with token": {contracts.AgentVoice{Agent: "bob", Token: botToken(t, "t"), Reply: contracts.ReplyInChannels}, false},
		"tokenless mention":   {contracts.AgentVoice{Agent: "bob", Reply: contracts.ReplyOnMention}, false},
		"tokenless bad name":  {contracts.AgentVoice{Agent: "-bob", Reply: contracts.ReplyInChannels}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := houseNeedsContent([]contracts.AgentVoice{tc.voice}); got != tc.want {
				t.Fatalf("houseNeedsContent = %v, want %v", got, tc.want)
			}
		})
	}
}
