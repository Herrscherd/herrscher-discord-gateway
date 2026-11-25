package discord

import contracts "github.com/Herrscherd/herrscher-contracts"

type turnEnding struct {
	prefix   headKey
	summary  headKey
	reaction headKey
	label    string
	announce bool
}

func endingOf(msgs messages, outcome contracts.TurnOutcome) turnEnding {
	switch outcome {
	case contracts.TurnFailed:
		return turnEnding{prefix: headFail, summary: headFail, reaction: headFail, label: msgs.say("discord.progress.failed", nil), announce: true}
	case contracts.TurnInterrupted:
		return turnEnding{prefix: headStop, summary: headStop, reaction: headStop, label: msgs.say("discord.progress.interrupted", nil)}
	}
	return turnEnding{summary: headDone, label: msgs.say("discord.progress.done", nil), announce: true}
}
