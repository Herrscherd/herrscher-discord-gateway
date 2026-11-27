package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
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
