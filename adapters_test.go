package discord

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonReply(body string) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	}
}

func clientOn(rt http.RoundTripper) *dctl.Client {
	return dctl.New("t", "chan", dctl.WithHTTPClient(&http.Client{Transport: rt}))
}

func TestPlatformReadNotesLastUserOnItsOwnChannelSink(t *testing.T) {
	f := &fakeRender{}
	set := newSinks(testRun(), f, staticLevels("full"), "", false, testLog())
	p := &Platform{sinks: set, c: clientOn(jsonReply(`[
		{"id":"9","channel_id":"c2","content":"d","author":{"id":"u2"}},
		{"id":"3","channel_id":"c1","content":"c","author":{"id":"u1"}},
		{"id":"2","channel_id":"c1","content":"b","author":{"id":"bot","bot":true}},
		{"id":"1","channel_id":"c1","content":"a","author":{"id":"u1"}}
	]`))}

	if _, err := p.Read(context.Background(), "c1", 100, ""); err != nil {
		t.Fatal(err)
	}
	if got := set.at("c1").lastUser; got != (msgRef{ch: "c1", id: "3", author: "u1"}) {
		t.Fatalf("c1 lastUser = %+v, want 3 in c1", got)
	}
	if got := set.at("c2").lastUser; got != (msgRef{ch: "c2", id: "9", author: "u2"}) {
		t.Fatalf("c2 lastUser = %+v, want 9 in c2, the ack must follow the message's own channel", got)
	}
}

func TestPlatformStillReadsAnUnboundChannel(t *testing.T) {
	p := &Platform{
		binds: newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog()),
		c:     clientOn(jsonReply(`[{"id":"1","channel_id":"c9","content":"x","author":{"id":"u1"}}]`)),
	}

	msgs, err := p.Read(context.Background(), "c9", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Read returned %d messages for an unbound channel, want 1", len(msgs))
	}
}
