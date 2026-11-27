package discord

import (
	"context"

	"github.com/Herrscherd/dctl"
)

type voicedClient struct{ bots *voiceBots }

func (v voicedClient) Send(ctx context.Context, conv, content string) (*dctl.Message, error) {
	target := v.bots.at(conv)
	text, err := target.frame(content)
	if err != nil {
		return nil, err
	}
	return discordClient{target.client}.Send(ctx, target.channel, text)
}

func (v voicedClient) Reply(ctx context.Context, conv, replyTo, content string) (*dctl.Message, error) {
	target := v.bots.at(conv)
	text, err := target.frame(content)
	if err != nil {
		return nil, err
	}
	return discordClient{target.client}.Reply(ctx, target.channel, replyTo, text)
}

func (v voicedClient) SendFiles(ctx context.Context, conv, content string, files []dctl.File) (*dctl.Message, error) {
	target := v.bots.at(conv)
	text, err := target.frame(content)
	if err != nil {
		return nil, err
	}
	return discordClient{target.client}.SendFiles(ctx, target.channel, text, files)
}

func (v voicedClient) ReplyFiles(ctx context.Context, conv, replyTo, content string, files []dctl.File) (*dctl.Message, error) {
	target := v.bots.at(conv)
	text, err := target.frame(content)
	if err != nil {
		return nil, err
	}
	return discordClient{target.client}.ReplyFiles(ctx, target.channel, replyTo, text, files)
}

func (v voicedClient) React(ctx context.Context, conv, messageID, emoji string) error {
	target := v.bots.at(conv)
	return discordClient{target.client}.React(ctx, target.channel, messageID, emoji)
}

func (v voicedClient) Unreact(ctx context.Context, conv, messageID, emoji string) error {
	target := v.bots.at(conv)
	return discordClient{target.client}.Unreact(ctx, target.channel, messageID, emoji)
}

func (v voicedClient) SendSelectMenu(ctx context.Context, conv, replyTo, content, customID string, options []dctl.SelectOption) (*dctl.Message, error) {
	target := v.bots.menuAt(conv)
	text, err := target.frame(content)
	if err != nil {
		return nil, err
	}
	return discordClient{target.client}.SendSelectMenu(ctx, target.channel, replyTo, text, customID, options)
}

func (v voicedClient) ReadMessages(ctx context.Context, conv string, limit int, after string) ([]dctl.Message, error) {
	target := v.bots.at(conv)
	return discordClient{target.client}.ReadMessages(ctx, target.channel, limit, after)
}

func (v voicedClient) GetMessage(ctx context.Context, conv, messageID string) (*dctl.Message, error) {
	target := v.bots.at(conv)
	return discordClient{target.client}.GetMessage(ctx, target.channel, messageID)
}

func (v voicedClient) CreatePrivateThread(ctx context.Context, conv, name string) (string, error) {
	target := v.bots.at(conv)
	return discordClient{target.client}.CreatePrivateThread(ctx, target.channel, name)
}

func (v voicedClient) AddThreadMember(ctx context.Context, thread, userID string) error {
	target := v.bots.at(thread)
	return discordClient{target.client}.AddThreadMember(ctx, target.channel, userID)
}

func (v voicedClient) StartThread(ctx context.Context, conv, messageID, name string) (string, error) {
	target := v.bots.at(conv)
	return discordClient{target.client}.StartThread(ctx, target.channel, messageID, name)
}

func (v voicedClient) EditMessage(ctx context.Context, conv, messageID, content string) (*dctl.Message, error) {
	target := v.bots.at(conv)
	text, err := target.frame(content)
	if err != nil {
		return nil, err
	}
	return discordClient{target.client}.EditMessage(ctx, target.channel, messageID, text)
}

func (v voicedClient) DeleteMessage(ctx context.Context, conv, messageID string) error {
	target := v.bots.at(conv)
	return discordClient{target.client}.DeleteMessage(ctx, target.channel, messageID)
}

func (v voicedClient) DeleteChannel(ctx context.Context, conv string) error {
	target := v.bots.at(conv)
	return discordClient{target.client}.DeleteChannel(ctx, target.channel)
}
