// Package discord is the Discord gateway of herrscher. It adapts the dctl client
// to the gateway ports of the host, routes the messages a channel receives to
// the sessions that answer them, and renders the turn events of each session
// into its Discord channel.
//
// It is a plugin module: it registers itself in one init, is compiled into the
// host with a blank import, and imports nothing of the host but its contracts
// module.
package discord
