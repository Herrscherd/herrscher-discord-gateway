package discord

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

type sentCall struct {
	path    string
	content string
}

type sendRecorder struct {
	calls []sentCall
}

func (rec *sendRecorder) client() *dctl.Client {
	return dctl.New("t", "chan", dctl.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body struct {
			Content string `json:"content"`
		}
		if req.Body != nil {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
		}
		rec.calls = append(rec.calls, sentCall{path: req.URL.Path, content: body.Content})
		return jsonReply("{}")(req)
	})}))
}

func TestTokenlessAgentSpeaksWithItsHeaderThroughTheHouseBot(t *testing.T) {
	house := &sendRecorder{}
	voiceClient := voicedClient{newVoiceBots(house.client())}

	if _, err := voiceClient.Send(context.Background(), "c1@ops_bot", "hi"); err != nil {
		t.Fatal(err)
	}
	if len(house.calls) != 1 || !strings.HasSuffix(house.calls[0].path, "/channels/c1/messages") {
		t.Fatalf("house calls = %+v, want one post in c1", house.calls)
	}
	if got := house.calls[0].content; got != "**ops\\_bot**\nhi" {
		t.Fatalf("content = %q, want the agent header then the text", got)
	}
}

func TestAgentWithItsOwnBotSpeaksThroughItsOwnClient(t *testing.T) {
	house, own := &sendRecorder{}, &sendRecorder{}
	bots := newVoiceBots(house.client())
	bots.register("bob", own.client(), "bot-app")

	if _, err := (voicedClient{bots}).Send(context.Background(), "c1@bob", "hi"); err != nil {
		t.Fatal(err)
	}
	if len(house.calls) != 0 {
		t.Fatalf("house bot posted for an agent with its own bot: %+v", house.calls)
	}
	if len(own.calls) != 1 || own.calls[0].content != "hi" {
		t.Fatalf("own bot calls = %+v, want the bare text through the agent's client", own.calls)
	}
}

func TestMenusAlwaysGoThroughTheHouseBot(t *testing.T) {
	house, own := &sendRecorder{}, &sendRecorder{}
	bots := newVoiceBots(house.client())
	bots.register("bob", own.client(), "bot-app")

	options := []dctl.SelectOption{{Label: "one", Value: "1"}}
	if _, err := (voicedClient{bots}).SendSelectMenu(context.Background(), "c1@bob", "", "pick", "dctlchoice:c1@bob", options); err != nil {
		t.Fatal(err)
	}
	if len(own.calls) != 0 {
		t.Fatalf("the agent's own bot posted a menu: %+v", own.calls)
	}
	if len(house.calls) != 1 || house.calls[0].content != "**bob**\npick" || !strings.HasSuffix(house.calls[0].path, "/channels/c1/messages") {
		t.Fatalf("house calls = %+v, want the menu from the house bot with the agent header", house.calls)
	}
}

const opsHeader = "**ops\\_bot**\n"

func voiceGateway(house *sendRecorder) *Gateway {
	bots := newVoiceBots(house.client())
	return &Gateway{c: voicedClient{bots}, voices: bots, log: testLog()}
}

func bodiesWithoutHeader(t *testing.T, calls []sentCall) string {
	t.Helper()
	bodies := make([]string, 0, len(calls))
	for _, call := range calls {
		if len(call.content) > gatewayMaxLen || !strings.HasPrefix(call.content, opsHeader) {
			t.Fatalf("call %+v, want at most %d characters, starting with the agent header", call, gatewayMaxLen)
		}
		bodies = append(bodies, strings.TrimPrefix(call.content, opsHeader))
	}
	return strings.Join(bodies, "")
}

func TestHeaderedSendSplitsWithinTheLimit(t *testing.T) {
	house := &sendRecorder{}
	g := voiceGateway(house)
	text := strings.Repeat("a", gatewayMaxLen)

	if _, err := g.SendFiles(context.Background(), contracts.Conversation{ID: "c1@ops_bot"}, "", text, nil); err != nil {
		t.Fatal(err)
	}

	if len(house.calls) != 2 {
		t.Fatalf("calls = %d, want the text split so each part fits with the header", len(house.calls))
	}
	if got := bodiesWithoutHeader(t, house.calls); got != text {
		t.Fatalf("the parts carry %d characters, want the whole %d back", len(got), len(text))
	}
}

func TestHeaderedPostSplitsWithinTheLimit(t *testing.T) {
	house := &sendRecorder{}
	bots := newVoiceBots(house.client())
	s := newSink(testRun(), renderAdapter{&Platform{bots: bots}}, "c1@ops_bot", nil, "", false, testMessages(), testLog())

	s.postParts(context.Background(), "c1@ops_bot", strings.Repeat("a", gatewayMaxLen))

	if len(house.calls) != 2 {
		t.Fatalf("calls = %d, want the rendered text split so each part fits with the header", len(house.calls))
	}
	bodiesWithoutHeader(t, house.calls)
}

func TestHeaderedEditRefusesWhatWouldPassTheLimit(t *testing.T) {
	house := &sendRecorder{}
	g := voiceGateway(house)

	if err := g.Edit(context.Background(), "c1@ops_bot", "m1", strings.Repeat("a", gatewayMaxLen)); !errors.Is(err, errTooLong) {
		t.Fatalf("Edit() = %v, want errTooLong for a text that would exceed the limit with its header", err)
	}
	if len(house.calls) != 0 {
		t.Fatalf("calls = %+v, want no edit sent", house.calls)
	}

	fits := strings.Repeat("a", gatewayMaxLen-len(opsHeader))
	if err := g.Edit(context.Background(), "c1@ops_bot", "m1", fits); err != nil {
		t.Fatal(err)
	}
	if got := bodiesWithoutHeader(t, house.calls); got != fits {
		t.Fatalf("edit carries %d characters, want the %d sent", len(got), len(fits))
	}
}
