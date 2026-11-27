package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
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
		content, err := messageContent(req)
		if err != nil {
			return nil, err
		}
		rec.calls = append(rec.calls, sentCall{path: req.URL.Path, content: content})
		return jsonReply("{}")(req)
	})}))
}

func messageContent(req *http.Request) (string, error) {
	var body struct {
		Content string `json:"content"`
	}
	if strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/") {
		if err := req.ParseMultipartForm(1 << 20); err != nil {
			return "", err
		}
		if err := json.Unmarshal([]byte(req.FormValue("payload_json")), &body); err != nil {
			return "", err
		}
		return body.Content, nil
	}
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && err != io.EOF {
			return "", err
		}
	}
	return body.Content, nil
}

type requestLog struct {
	calls []string
}

func (rec *requestLog) client() *dctl.Client {
	return dctl.New("t", "chan", dctl.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec.calls = append(rec.calls, req.Method+" "+req.URL.Path)
		if req.Method == http.MethodDelete {
			return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
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
	return &Gateway{c: voicedClient{bots}, voices: bots, log: testLog(), msgs: testMessages(), fileLimit: maxFileBytes, batchLimit: maxBatchBytes}
}

func bodiesWithoutHeader(t *testing.T, calls []sentCall) string {
	t.Helper()
	bodies := make([]string, 0, len(calls))
	for _, call := range calls {
		if utf8.RuneCountInString(call.content) > gatewayMaxLen || !strings.HasPrefix(call.content, opsHeader) {
			t.Fatalf("call %+v, want at most %d characters, starting with the agent header", call, gatewayMaxLen)
		}
		bodies = append(bodies, strings.TrimPrefix(call.content, opsHeader))
	}
	return strings.Join(bodies, "")
}

func TestHeaderedSendFilesSplitsWithinTheLimit(t *testing.T) {
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

func TestVoicedTextSplitsWithItsHeaderCounted(t *testing.T) {
	const conv = "c1@ops_bot"
	text := strings.Repeat("a", gatewayMaxLen-len(opsHeader)+1)
	file := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outbound := []contracts.OutboundFile{{Path: file, Name: "note.txt", ContentType: "text/plain"}}
	ops := map[string]func(g *Gateway) error{
		"Post": func(g *Gateway) error {
			_, err := g.Post(context.Background(), contracts.Conversation{ID: conv}, text)
			return err
		},
		"Reply": func(g *Gateway) error {
			_, err := g.Reply(context.Background(), contracts.Conversation{ID: conv}, "m1", text)
			return err
		},
		"SendFiles without an answer": func(g *Gateway) error {
			_, err := g.SendFiles(context.Background(), contracts.Conversation{ID: conv}, "", text, nil)
			return err
		},
		"ReplyFiles": func(g *Gateway) error {
			_, err := g.SendFiles(context.Background(), contracts.Conversation{ID: conv}, "m1", text, outbound)
			return err
		},
		"ChannelAdmin.Send": func(g *Gateway) error {
			return NewChannelAdmin(g.voices).Send(context.Background(), conv, text)
		},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			house := &sendRecorder{}
			g := voiceGateway(house)
			if err := op(g); err != nil {
				t.Fatal(err)
			}
			if len(house.calls) != 2 {
				t.Fatalf("calls = %d, want the text split in two so each part fits with the header", len(house.calls))
			}
			if got := bodiesWithoutHeader(t, house.calls); got != text {
				t.Fatalf("the parts carry %d characters, want the whole %d back", len(got), len(text))
			}
		})
	}
}

func TestVoicedNoticeSplitsWithItsHeaderCounted(t *testing.T) {
	house := &sendRecorder{}
	bots := newVoiceBots(house.client())
	g := NewGateway(voicedClient{bots}, testRun(), testMessages(), testLog())
	g.voices = bots
	notice := contracts.Notice{Code: contracts.CodeSettingInvalid, Args: []contracts.Arg{contracts.Text("Setting", "ops"), contracts.Text("Value", strings.Repeat("a", gatewayMaxLen))}}

	if _, err := g.Notify(context.Background(), contracts.Conversation{ID: "c1@ops_bot"}, i18n.English, notice); err != nil {
		t.Fatal(err)
	}
	if len(house.calls) < 2 {
		t.Fatalf("calls = %d, want the notice split so each part fits with the header", len(house.calls))
	}
	bodiesWithoutHeader(t, house.calls)
}

func TestHeaderedStatusRefusesWhatWouldPassTheLimit(t *testing.T) {
	house := &sendRecorder{}
	plat := &Platform{bots: newVoiceBots(house.client())}
	conv := "c1@ops_bot"

	if _, err := plat.UpsertStatusMessage(context.Background(), conv, "m1", strings.Repeat("a", gatewayMaxLen)); !errors.Is(err, errTooLong) {
		t.Fatalf("UpsertStatusMessage() = %v, want errTooLong for a status that would exceed the limit with its header", err)
	}
	if len(house.calls) != 0 {
		t.Fatalf("calls = %+v, want no status sent", house.calls)
	}

	fits := strings.Repeat("a", gatewayMaxLen-len(opsHeader))
	if _, err := plat.UpsertStatusMessage(context.Background(), conv, "m1", fits); err != nil {
		t.Fatal(err)
	}
	if got := bodiesWithoutHeader(t, house.calls); got != fits {
		t.Fatalf("status carries %d characters, want the %d sent", len(got), len(fits))
	}
}

func TestVoicedProgressFitsTheLimitWithItsHeader(t *testing.T) {
	house := &sendRecorder{}
	const conv = "c1@ops_bot"
	plat := &Platform{bots: newVoiceBots(house.client())}
	pv := newProgressView(func(id, content string) (string, error) {
		return plat.UpsertStatusMessage(context.Background(), conv, id, content)
	}, levelFull, time.Unix(0, 0), testMessages(), testLog())
	pv.maxLen = renderAdapter{plat}.TextLimit(conv)

	for i := range maxLines * 2 {
		pv.add(contracts.BackendEvent{Kind: "tool", Tool: "Bash", Detail: strconv.Itoa(i) + strings.Repeat("x", 200)})
	}
	pv.flush(true)

	if len(house.calls) == 0 {
		t.Fatal("no progress status sent")
	}
	bodiesWithoutHeader(t, house.calls)
}

func TestTidyPingIsDeletedByTheHouseBot(t *testing.T) {
	house, own := &requestLog{}, &requestLog{}
	bots := newVoiceBots(house.client())
	bots.register("bob", own.client(), "bot-app")
	s := newSink(testRun(), renderAdapter{&Platform{bots: bots}}, "c1@bob", nil, "u1", true, testMessages(), testLog())
	s.acked = msgRef{ch: "c1@bob", id: "m9", author: "u1"}

	if !s.tidyPing(context.Background(), "c1@bob") {
		t.Fatal("tidyPing() = false, want the opening ping of the owner deleted")
	}
	if len(house.calls) != 1 || !strings.HasSuffix(house.calls[0], "/channels/c1/messages/m9") || !strings.HasPrefix(house.calls[0], http.MethodDelete) {
		t.Fatalf("house calls = %+v, want the ping deleted by the house bot in c1", house.calls)
	}
	if len(own.calls) != 0 {
		t.Fatalf("the voice's own bot made calls %+v, want the deletion left to the house bot", own.calls)
	}
}
