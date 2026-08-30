package discord

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts"
)

// skills carries the playbook that teaches an agent when to reach for the verbs
// this gateway contributes. It is embedded, and hung on the Plugin rather than on
// a built GatewaySet, on purpose: the skill installs on a machine where this
// gateway is compiled in, token or no token, and nowhere else. A Discord playbook
// on a host with no Discord is noise in every agent's context forever, for a
// capability that does not exist there.
//
//go:embed skills
var skillsFS embed.FS

// skills is that tree rooted at the skills dir, so its entries are the skill
// names the host installs ("discord-conversations/SKILL.md") and not a "skills/"
// prefix leaking this repo's layout into ~/.claude/skills.
var skills = func() fs.FS {
	sub, err := fs.Sub(skillsFS, "skills")
	if err != nil {
		panic("discord gateway: embedded skills tree: " + err.Error())
	}
	return sub
}()

// init self-registers the Discord gateway into the global plugin registry. A
// blank import of this package (in the host's generated plugins.go) is enough to
// make the gateway discoverable — no wiring in the host. The factory builds its
// own dctl client from config so the plugin stays self-contained.
func init() {
	contracts.Register(contracts.Plugin{
		Manifest: contracts.Manifest{
			Kind:         "discord",
			Category:     contracts.CategoryGateway,
			Status:       contracts.StatusLive,
			Capabilities: contracts.Capabilities{Reactions: true, SelectMenus: true, Replies: true},
			Config: []contracts.Setting{
				{Key: "token", Env: "DISCORD_BOT_TOKEN", Help: "Discord bot token", Required: true},
				{Key: "owner", Env: "DISCORD_USER_ID", Help: "Discord user id that is always allowed, whatever the allowlists say"},
				{Key: "allowed_users", Env: "DISCORD_ALLOWED_USERS", Help: "comma-separated Discord user ids allowed to make the bot act (`*` for everyone)"},
				{Key: "allowed_roles", Env: "DISCORD_ALLOWED_ROLES", Help: "comma-separated Discord role ids allowed to make the bot act (guild messages only; a DM carries no role)"},
				{Key: "allowed_channels", Env: "DISCORD_ALLOWED_CHANNELS", Help: "comma-separated channel ids the bot answers in, ignoring every other one (empty = every channel it can read)"},
				{Key: "ignored_channels", Env: "DISCORD_IGNORED_CHANNELS", Help: "comma-separated channel ids the bot never answers in, whatever the rest allows"},
				{Key: "allow_all_users", Env: "DISCORD_ALLOW_ALL_USERS", Help: "let anyone who can reach the bot make it act (default off; the allowlists are what a deployment is normally gated on)"},
				{Key: "allow_bots", Env: "DISCORD_ALLOW_BOTS", Help: "whether other bots may make this one act: none (default) | mentions (only when they @mention it) | all"},
				{Key: "require_mention", Env: "DISCORD_REQUIRE_MENTION", Help: "require an @mention in a channel (default true; a DM and a thread the gateway opened never need one)"},
				{Key: "thread_require_mention", Env: "DISCORD_THREAD_REQUIRE_MENTION", Help: "require an @mention inside a thread the gateway opened too (default false)"},
				{Key: "free_response_channels", Env: "DISCORD_FREE_RESPONSE_CHANNELS", Help: "comma-separated channel ids where the bot answers without being @mentioned (`*` for every channel; who may then make it act is still the allowlists)"},
				{Key: "sessions_per_user", Env: "DISCORD_SESSIONS_PER_USER", Help: "give each participant of a channel their own session (default true; false puts the whole room in one shared session)"},
				{Key: "thread_sessions_per_user", Env: "DISCORD_THREAD_SESSIONS_PER_USER", Help: "give each participant of a thread their own session (default false; a thread is one piece of work everyone in it shares)"},
				{Key: "agent", Env: "DISCORD_AGENT", Help: "persona every session this gateway opens runs as, which is what gates its tools (required by /mode ambient)"},
				{Key: "verbosity", Env: "DISCORD_VERBOSITY", Help: "default for how much of a turn shows in-channel, overridable per conversation with /verbosity: silent (default; the answer and nothing else) | quiet (adds a live list of tool names) | actions (adds each tool's detail) | full (adds the assistant's text)"},
				{Key: "context_messages", Env: "DISCORD_CONTEXT_MESSAGES", Help: "how many prior channel messages to carry as context (default 30)"},
				{Key: "playbook", Env: "DISCORD_PLAYBOOK", Help: "skill name a new session is told to follow (default pr-job)"},
				{Key: "tidy_pings", Env: "DISCORD_TIDY_PINGS", Help: "delete the message that opened a turn once its answer is posted, so a channel keeps the work and not the requests (default off; never applies where the answer lives in a thread started off the ping)"},
				{Key: "message_deletes", Env: "DISCORD_MESSAGE_DELETES", Help: "contribute the `message delete` verb, so an agent can delete messages this bot sent (default off; the verb is absent entirely until this is set, and even then it refuses any message another author wrote)"},
			},
		},
		Gateway: NewGatewaySet,
		Skills:  shipSkills,
	})
}

// shipSkills hands the embedded tree over unconditionally. The contract asks for
// a factory so a plugin can look at the machine and decline; this one has nothing
// to look at — where the gateway is compiled in, its playbook belongs.
func shipSkills(context.Context, contracts.PluginConfig) (fs.FS, error) { return skills, nil }

// NewGatewaySet builds the Discord channel from config: it wires the outbound
// gateway, the read/status reader, the channel admin and the reachability prober.
func NewGatewaySet(ctx context.Context, cfg contracts.PluginConfig) (contracts.GatewaySet, error) {
	token := cfg.Get("token")
	owner := cfg.Get("owner")
	// No default channel: the bot is bound to an operator, not to a room, and it
	// always answers in the conversation it was addressed in. Global commands for
	// the same reason — the bot follows its owner across every server it is in,
	// so its slash surface cannot belong to one of them.
	c := dctl.New(token, "", dctl.WithGlobalCommands())
	gw := NewGateway(discordClient{c})
	plat := NewPlatform(c)
	binds := newBindStore(bindStorePath())
	plat.binds = binds

	// One shared set of per-conversation renderers: the gateway feeds it routed
	// events (EmitTo) and the router records the triggering message id for the ACK
	// of the conversation that message belongs to.
	//
	// DISCORD_VERBOSITY is only the default a conversation falls back on: `/verbosity`
	// retunes one room without touching the others and without a daemon restart,
	// so the level is read from the store on every turn, not captured here.
	level := verbositySetting(cfg.Get("verbosity"))
	s := newSinks(ctx, renderAdapter{plat}, func(conv string) string {
		if v := binds.Level(conv); v != "" {
			return v
		}
		return level
	}, owner, boolSetting(cfg.Get("tidy_pings")))
	gw.sinks = s
	plat.sinks = s

	appID, err := c.Interactions().AppID(ctx)
	if err != nil {
		return contracts.GatewaySet{}, fmt.Errorf("discord gateway: resolve application id: %w", err)
	}

	// The delete verb is contributed only where an operator asked for it, and it
	// needs the bot's own id to tell its own messages from everyone else's — the
	// same id the mention trigger matches on.
	gw.deletes = boolSetting(cfg.Get("message_deletes"))
	gw.selfID = appID

	// The slash surface lives entirely in the plugin: it builds its own dctl
	// command catalog + allow store and only crosses the boundary through the
	// neutral SessionControl seam bound later (BindSessionControl).
	gw.slash = newSlash(ctx, c.Interactions(), c.Components(), token, newAllowStore(allowStorePath()), binds)
	// The router reads the controller through a closure because it is bound after
	// the plugin is built (BindSessionControl), not before.
	gw.slash.router = newRouter(
		func() contracts.SessionControl { return gw.slash.ctrl },
		discordClient{c}, binds, s,
		routerConfig{
			owner:           owner,
			appID:           appID,
			contextMessages: intSetting(cfg.Get("context_messages"), 30),
			playbook:        strSetting(cfg.Get("playbook"), "pr-job"),
			agent:           cfg.Get("agent"),
			access: accessSettings{
				users:    newIDSet(cfg.Get("allowed_users")),
				roles:    newIDSet(cfg.Get("allowed_roles")),
				channels: newIDSet(cfg.Get("allowed_channels")),
				ignored:  newIDSet(cfg.Get("ignored_channels")),
				allowAll: boolSetting(cfg.Get("allow_all_users")),
				bots:     botsSetting(cfg.Get("allow_bots")),
			},
			address: addressPolicy{
				appID:                appID,
				requireMention:       switchSetting(cfg.Get("require_mention"), true),
				threadRequireMention: switchSetting(cfg.Get("thread_require_mention"), false),
				free:                 newIDSet(cfg.Get("free_response_channels")),
			},
			scope: sessionScope{
				groupPerUser:  switchSetting(cfg.Get("sessions_per_user"), true),
				threadPerUser: switchSetting(cfg.Get("thread_sessions_per_user"), false),
			},
		},
	)
	return contracts.GatewaySet{
		Gateway: gw,
		Reader:  plat,
		Admin:   NewChannelAdmin(c),
		Prober:  NewProber(c),
	}, nil
}

// stateDir is the gateway's own data dir: beside the daemon state
// (DCTL_STATE_DIR) so it shares the instance's data dir, falling back to
// ~/.config/dctl.
func stateDir() string {
	dir := os.Getenv("DCTL_STATE_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config", "dctl")
	}
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// allowStorePath is where the gateway persists its permission store.
func allowStorePath() string { return filepath.Join(stateDir(), "discord-allow.json") }

// bindStorePath sits beside the allow store, under the same state dir.
func bindStorePath() string { return filepath.Join(stateDir(), "discord-router.json") }

// intSetting parses a numeric setting, falling back to def for empty or invalid
// values — a typo must not silently disable channel context.
func intSetting(v string, def int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// knownLevel reports whether v is one of the four render levels.
func knownLevel(v string) bool {
	switch v {
	case levelSilent, levelQuiet, levelActions, levelFull:
		return true
	}
	return false
}

// verbositySetting maps the configured render level onto the four the renderer
// knows, falling back to silent for anything else. Silent is the only safe
// default: the bot is meant to be pinged in channels other people read, where
// a live list of tools, paths and shell commands is both noise and machine
// layout. Showing more than the answer has to be asked for, and a typo must not
// turn it on by accident.
func verbositySetting(v string) string {
	if knownLevel(v) {
		return v
	}
	return defaultLevel
}

// boolSetting reads an opt-in switch. Only the spellings people actually write
// count as on; anything else — including a typo — leaves it off, which is what a
// setting that deletes messages has to do when it is not sure.
func boolSetting(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func switchSetting(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func botsSetting(v string) string {
	if lower := strings.ToLower(strings.TrimSpace(v)); knownBotPolicy(lower) {
		return lower
	}
	return botsNone
}

func strSetting(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
