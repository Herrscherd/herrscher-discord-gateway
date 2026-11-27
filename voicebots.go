package discord

import (
	"sync"

	"github.com/Herrscherd/dctl"
)

type ownBot struct {
	client *dctl.Client
	appID  string
}

type voiceBots struct {
	house *dctl.Client

	mu  sync.Mutex
	own map[string]ownBot
}

func newVoiceBots(house *dctl.Client) *voiceBots {
	return &voiceBots{house: house, own: make(map[string]ownBot)}
}

func (b *voiceBots) register(agent string, client *dctl.Client, appID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.own[agent] = ownBot{client: client, appID: appID}
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

type speaker struct {
	client  *dctl.Client
	channel string
	header  string
}

func (b *voiceBots) at(conv string) speaker {
	addr := addressOf(conv)
	if bot, ok := b.ownOf(addr.agent); ok {
		return speaker{client: bot.client, channel: addr.channel}
	}
	return speaker{client: b.house, channel: addr.channel, header: agentHeader(addr.agent)}
}

func (b *voiceBots) menuAt(conv string) speaker {
	addr := addressOf(conv)
	return speaker{client: b.house, channel: addr.channel, header: agentHeader(addr.agent)}
}
