package discord

import (
	"context"
	"fmt"
	"sync"
	"unicode/utf8"

	"github.com/Herrscherd/dctl"
)

type ownBot struct {
	client *dctl.Client
	appID  string
}

type voiceBots struct {
	house *dctl.Client

	mu         sync.Mutex
	own        map[string]ownBot
	resolving  map[string]bool
	generation uint64
}

func newVoiceBots(house *dctl.Client) *voiceBots {
	return &voiceBots{house: house, own: make(map[string]ownBot), resolving: make(map[string]bool)}
}

func (b *voiceBots) register(agent string, client *dctl.Client, appID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.own[agent] = ownBot{client: client, appID: appID}
	b.generation++
}

func (b *voiceBots) version() uint64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.generation
}

func (b *voiceBots) expect(agents []string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, agent := range agents {
		b.resolving[agent] = true
	}
	b.generation++
}

func (b *voiceBots) settle(agent string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.resolving, agent)
	b.generation++
}

func (b *voiceBots) resolvingAgent(agent string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resolving[agent]
}

func (b *voiceBots) awaiting() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.resolving) > 0
}

func (b *voiceBots) ownOf(agent string) (ownBot, bool) {
	if b == nil || agent == "" {
		return ownBot{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	bot, ok := b.own[agent]
	return bot, ok
}

func (b *voiceBots) owns(userID string) bool {
	if b == nil || userID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, bot := range b.own {
		if bot.appID == userID {
			return true
		}
	}
	return false
}

func (b *voiceBots) agentAppIDs() []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]string, 0, len(b.own))
	for _, bot := range b.own {
		ids = append(ids, bot.appID)
	}
	return ids
}

func (b *voiceBots) headerOf(conv string) string {
	agent := addressOf(conv).agent
	if _, ok := b.ownOf(agent); ok {
		return ""
	}
	return agentHeader(agent)
}

func (b *voiceBots) textLimit(conv string) int {
	return gatewayMaxLen - utf8.RuneCountInString(b.headerOf(conv))
}

func (b *voiceBots) menuLimit(conv string) int {
	return gatewayMaxLen - utf8.RuneCountInString(agentHeader(addressOf(conv).agent))
}

type speaker struct {
	client  *dctl.Client
	channel string
	header  string
}

func (s speaker) limit() int {
	return gatewayMaxLen - utf8.RuneCountInString(s.header)
}

func (s speaker) frame(content string) (string, error) {
	text := s.header + content
	if n := utf8.RuneCountInString(text); n > gatewayMaxLen {
		return "", fmt.Errorf("%w: %d characters, the limit is %d", errTooLong, n, gatewayMaxLen)
	}
	return text, nil
}

func (s speaker) post(ctx context.Context, content string) error {
	text, err := s.frame(content)
	if err != nil {
		return err
	}
	_, err = s.client.Messages().Send(ctx, s.channel, text)
	return err
}

func (b *voiceBots) at(conv string) speaker {
	addr := addressOf(conv)
	if bot, ok := b.ownOf(addr.agent); ok {
		return speaker{client: bot.client, channel: addr.channel}
	}
	return speaker{client: b.house, channel: addr.channel, header: agentHeader(addr.agent)}
}

func (b *voiceBots) houseAt(conv string) speaker {
	return speaker{client: b.house, channel: addressOf(conv).channel}
}

func (b *voiceBots) menuAt(conv string) speaker {
	addr := addressOf(conv)
	return speaker{client: b.house, channel: addr.channel, header: agentHeader(addr.agent)}
}

func splitText(text string, limit int) []string {
	parts := chunkText(text, limit)
	if len(parts) == 0 {
		return []string{text}
	}
	return parts
}
