package discord

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

// attachmentHosts are the hosts a Discord file or embed image lives on. The
// manifest declares them so the host will download them at all — it pins every
// attachment url to a gateway-supplied allowlist rather than fetching whatever a
// remote author wrote — and the same list filters embed images on the way out:
// an embed can name any url on the internet, and handing over one the host is
// bound to refuse only burns a slot under its per-message cap.
var attachmentHosts = []string{
	"cdn.discordapp.com",          // what an upload is served from
	"media.discordapp.net",        // Discord's own copy of an embed image
	"images-ext-1.discordapp.net", // and of an image it proxied from elsewhere
	"images-ext-2.discordapp.net",
}

var attachmentHost = func() map[string]bool {
	m := make(map[string]bool, len(attachmentHosts))
	for _, h := range attachmentHosts {
		m[h] = true
	}
	return m
}()

// reference resolves the message this ping replies to. "@Herrscher regarde ça"
// under someone's bug report says nothing on its own — the report is the request
// — so the reply target is read and carried into the turn.
//
// The dispatch names it but blanks its body, exactly as it blanks every message
// that does not @mention the app, and hydrate cannot help: it looks in the last
// few messages of the channel, while a reply target is whatever the operator
// scrolled back to. REST is not gated by the intent, so the message is re-read
// by id. It returns nil when there is nothing to carry.
func (r *router) reference(ctx context.Context, m messageCreate) *dctl.Message {
	if m.Referenced == nil {
		return nil
	}
	ref := m.Referenced
	if carries(*ref) {
		return ref
	}
	if ref.ID == "" {
		return nil
	}
	// A reply resolves in the channel the target lives in, which is the current
	// one unless Discord said otherwise.
	channel := ref.ChannelID
	if channel == "" {
		channel = m.ChannelID
	}
	got, err := r.c.GetMessage(ctx, channel, ref.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: read replied-to message: %v\n", err)
		return nil
	}
	if got == nil || !carries(*got) {
		return nil
	}
	return got
}

// carries reports whether a message has anything worth showing the agent. A bot
// posts with an empty body and everything in an embed, so text alone is not the
// test.
func carries(m dctl.Message) bool {
	return strings.TrimSpace(m.Content) != "" || len(m.Attachments) > 0 || len(m.Embeds) > 0
}

// quote renders the replied-to message at the head of the turn, marked as
// somebody else's words rather than the operator's instruction.
func quote(ref *dctl.Message) string {
	if ref == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Message auquel il répond — %s :\n", ref.Author.Username)
	if body := strings.TrimSpace(ref.Content); body != "" {
		for _, line := range strings.Split(body, "\n") {
			fmt.Fprintf(&b, "> %s\n", line)
		}
	}
	for _, line := range describe(*ref) {
		fmt.Fprintf(&b, "> %s\n", line)
	}
	return b.String()
}

// describeMax bounds one rendered line. An embed description runs to 4096
// characters and a field value to 1024, and the channel context renders every
// embed of the last DISCORD_CONTEXT_MESSAGES messages — a channel of verbose bot
// reports would otherwise be most of the turn.
const describeMax = 600

// describe renders what a message carries besides its text: the files on it, and
// the embeds a bot or a webhook posts instead of writing anything. Without this
// a channel driven by bot reports reads as a wall of empty messages.
//
// Every line is flattened to one line: both call sites frame a line by prefixing
// it, with "> " for a quote and with an author name for the channel context, and
// an embed body full of newlines would walk straight out of that frame.
func describe(m dctl.Message) []string {
	var out []string
	add := func(s string) {
		if s = clip(flatten(s), describeMax); s != "" {
			out = append(out, s)
		}
	}
	for _, a := range m.Attachments {
		add("[fichier] " + a.Filename)
	}
	for _, e := range m.Embeds {
		if head := embedHead(e); head != "" {
			add("[embed] " + head)
		}
		for _, f := range e.Fields {
			if f.Name == "" && f.Value == "" {
				continue
			}
			add(fmt.Sprintf("[embed] %s : %s", f.Name, f.Value))
		}
		if u := mediaURL(e.Image); u != "" {
			add("[image] " + label(u))
		} else if u := mediaURL(e.Thumbnail); u != "" {
			add("[image] " + label(u))
		}
	}
	return out
}

func embedHead(e dctl.Embed) string {
	var parts []string
	if e.Title != "" {
		parts = append(parts, e.Title)
	}
	if e.Description != "" {
		parts = append(parts, e.Description)
	}
	head := strings.Join(parts, " — ")
	if e.URL != "" {
		if head == "" {
			return e.URL
		}
		return head + " (" + e.URL + ")"
	}
	return head
}

// mediaURL picks which of an embed image's two urls to use. Discord's own copy
// is preferred over the original: a link preview's image lives on a third-party
// site, and only the copy is somewhere the host will agree to download from.
func mediaURL(m *dctl.EmbedMedia) string {
	if m == nil {
		return ""
	}
	if m.ProxyURL != "" {
		return m.ProxyURL
	}
	return m.URL
}

// label names an image in the turn text. One that is being handed over as an
// attachment is named by its filename — the agent has the file — and one that is
// not gets its url, which is the only trace of it left.
func label(u string) string {
	if name, ok := cdnName(u); ok {
		return name
	}
	return u
}

// cdnName reads the filename out of a Discord media url and reports whether the
// url is on the CDN at all. Discord signs its media urls
// (`…/shot.png?ex=…&is=…&hm=…`), so the query has to come off before the
// extension is readable — and the extension is what the host falls back on to
// decide it is looking at an image, since an embed declares no content type.
func cdnName(raw string) (string, bool) {
	u, err := url.Parse(raw)
	// A host name is case-insensitive, and this is a map lookup — as is the
	// host's own check against the very list this gateway declares.
	if err != nil || u.Scheme != "https" || !attachmentHost[strings.ToLower(u.Hostname())] {
		return "", false
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		return "", false
	}
	return name, true
}

// attachmentsOf maps everything this turn should be able to look at into the
// neutral shape. Order is priority order, because the host caps how many files
// it downloads per message: the ping's own uploads first — the operator chose
// those deliberately — then the files on the message it replies to, then the
// images inside that message's embeds, which is where a bot puts its
// screenshots. The host, not the gateway, downloads them.
func attachmentsOf(m messageCreate, ref *dctl.Message) []contracts.Attachment {
	out := uploads(m.Attachments)
	if ref == nil {
		return out
	}
	out = append(out, uploads(ref.Attachments)...)
	return append(out, embedImages(*ref)...)
}

func uploads(as []dctl.Attachment) []contracts.Attachment {
	if len(as) == 0 {
		return nil
	}
	out := make([]contracts.Attachment, 0, len(as))
	for _, a := range as {
		out = append(out, contracts.Attachment{
			Filename:    a.Filename,
			URL:         a.URL,
			ContentType: a.ContentType,
			Size:        a.Size,
		})
	}
	return out
}

// embedImages hands over the pictures inside a message's embeds. An embed
// declares no content type and no size, so the filename carries the whole
// signal — hence cdnName, which is also what keeps an off-CDN url from being
// forwarded at all. One image per embed: a thumbnail is the same picture
// smaller, and downloading both would spend the host's cap twice on it. Two
// embeds naming the same picture spend it twice as well, so a url is handed over
// once — a bot that reports in several embeds off one screenshot is the ordinary
// case, not a corner one.
func embedImages(m dctl.Message) []contracts.Attachment {
	var out []contracts.Attachment
	seen := map[string]bool{}
	for _, e := range m.Embeds {
		u := mediaURL(e.Image)
		if u == "" {
			u = mediaURL(e.Thumbnail)
		}
		name, ok := cdnName(u)
		if !ok || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, contracts.Attachment{Filename: name, URL: u})
	}
	return out
}
