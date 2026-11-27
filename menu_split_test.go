package discord

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

type menuCall struct {
	content   string
	menu      bool
	reference string
}

type menuRecorder struct {
	calls     []menuCall
	failFirst bool
}

func (rec *menuRecorder) client() *dctl.Client {
	return dctl.New("t", "chan", dctl.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body struct {
			Content    string            `json:"content"`
			Components []json.RawMessage `json:"components"`
			Reference  *struct {
				MessageID string `json:"message_id"`
			} `json:"message_reference"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		call := menuCall{content: body.Content, menu: len(body.Components) > 0}
		if body.Reference != nil {
			call.reference = body.Reference.MessageID
		}
		rec.calls = append(rec.calls, call)
		if rec.failFirst && len(rec.calls) == 1 {
			return nil, errors.New("discord refused the part")
		}
		return jsonReply("{}")(req)
	})}))
}

func menuChoices() []contracts.Choice {
	return []contracts.Choice{{Label: "A", Value: "a"}}
}

func TestAMenuWhoseHeaderedPromptIsTooLongSendsTheLeadAsMessages(t *testing.T) {
	house := &menuRecorder{}
	bots := newVoiceBots(house.client())
	g := &Gateway{c: voicedClient{bots}, voices: bots, log: testLog()}
	prompt := strings.Repeat("a", gatewayMaxLen)

	if err := g.Menu(context.Background(), contracts.Conversation{ID: "c1@ops_bot"}, "m1", prompt, menuChoices()); err != nil {
		t.Fatalf("Menu() = %v, want the prompt split so that the menu fits", err)
	}

	if len(house.calls) < 2 {
		t.Fatalf("calls = %+v, want the prompt split over at least two messages", house.calls)
	}
	last := len(house.calls) - 1
	var bodies []string
	for index, call := range house.calls {
		if utf8.RuneCountInString(call.content) > gatewayMaxLen || !strings.HasPrefix(call.content, opsHeader) {
			t.Fatalf("call %d = %+v, want at most %d characters, starting with the agent header", index, call, gatewayMaxLen)
		}
		if call.menu != (index == last) {
			t.Fatalf("call %d carries the menu = %v, want only the last message to carry it", index, call.menu)
		}
		bodies = append(bodies, strings.TrimPrefix(call.content, opsHeader))
	}
	if got := strings.Join(bodies, ""); got != prompt {
		t.Fatalf("the parts carry %d characters, want the whole %d back", len(got), len(prompt))
	}
}

func TestAMenuForAnAgentWithItsOwnBotFitsWithTheHouseHeader(t *testing.T) {
	house, own := &menuRecorder{}, &menuRecorder{}
	bots := newVoiceBots(house.client())
	bots.register("bob", own.client(), "bot-app")
	g := &Gateway{c: voicedClient{bots}, voices: bots, log: testLog()}
	prompt := strings.Repeat("a", gatewayMaxLen)

	if err := g.Menu(context.Background(), contracts.Conversation{ID: "c1@bob"}, "m1", prompt, menuChoices()); err != nil {
		t.Fatalf("Menu() = %v, want the prompt split so that the menu fits the house header", err)
	}

	if len(house.calls) != 1 || !house.calls[0].menu {
		t.Fatalf("house calls = %+v, want exactly the menu from the house bot", house.calls)
	}
	if utf8.RuneCountInString(house.calls[0].content) > gatewayMaxLen || !strings.HasPrefix(house.calls[0].content, "**bob**\n") {
		t.Fatalf("menu = %q, want at most %d characters, starting with bob's header", house.calls[0].content, gatewayMaxLen)
	}
	var bodies []string
	for _, call := range own.calls {
		if call.menu {
			t.Fatalf("the agent's own bot posted a menu: %+v", own.calls)
		}
		bodies = append(bodies, call.content)
	}
	bodies = append(bodies, strings.TrimPrefix(house.calls[0].content, "**bob**\n"))
	if got := strings.Join(bodies, ""); got != prompt {
		t.Fatalf("the parts carry %d characters, want the whole %d back", len(got), len(prompt))
	}
}

func TestAMenuAfterLeadPartsDoesNotAnswerTheTrigger(t *testing.T) {
	house := &menuRecorder{}
	bots := newVoiceBots(house.client())
	g := &Gateway{c: voicedClient{bots}, voices: bots, log: testLog()}
	prompt := strings.Repeat("a", gatewayMaxLen*3)

	if err := g.Menu(context.Background(), contracts.Conversation{ID: "c1@ops_bot"}, "m1", prompt, menuChoices()); err != nil {
		t.Fatal(err)
	}

	if len(house.calls) < 2 || house.calls[0].reference != "m1" {
		t.Fatalf("calls = %+v, want the first lead part answering m1", house.calls)
	}
	last := house.calls[len(house.calls)-1]
	if !last.menu || last.reference != "" {
		t.Fatalf("menu call = %+v, want the menu posted without a reply once lead parts went out", last)
	}
}

func TestAShortMenuIsOneMessageWithTheMenu(t *testing.T) {
	house := &menuRecorder{}
	bots := newVoiceBots(house.client())
	g := &Gateway{c: voicedClient{bots}, voices: bots, log: testLog()}

	if err := g.Menu(context.Background(), contracts.Conversation{ID: "c1@ops_bot"}, "m1", "pick", menuChoices()); err != nil {
		t.Fatal(err)
	}
	if len(house.calls) != 1 || !house.calls[0].menu || house.calls[0].content != opsHeader+"pick" {
		t.Fatalf("calls = %+v, want one menu message carrying the header and the prompt", house.calls)
	}
}

func TestAMenuIsNotPostedWhenALeadPartFails(t *testing.T) {
	house := &menuRecorder{failFirst: true}
	bots := newVoiceBots(house.client())
	g := &Gateway{c: voicedClient{bots}, voices: bots, log: testLog()}
	prompt := strings.Repeat("a", gatewayMaxLen*3)

	if err := g.Menu(context.Background(), contracts.Conversation{ID: "c1@ops_bot"}, "m1", prompt, menuChoices()); err == nil {
		t.Fatal("Menu() = nil, want the failed lead part returned")
	}

	if len(house.calls) != 1 || house.calls[0].menu {
		t.Fatalf("calls = %+v, want only the failed first part: nothing is sent after it and no menu follows", house.calls)
	}
}
