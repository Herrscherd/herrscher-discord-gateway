package discord

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

//go:embed skills
var skillsFS embed.FS

const (
	defaultContextMessages = 30
	stateDirPerm           = 0o700
)

func embeddedSkills() (fs.FS, error) {
	sub, err := fs.Sub(skillsFS, "skills")
	if err != nil {
		return nil, fmt.Errorf("discord gateway: embedded skills tree: %w", err)
	}
	return sub, nil
}

func init() {
	contracts.Register(contracts.Plugin{
		Manifest: contracts.Manifest{
			Kind:         gatewayKind,
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
				{Key: "verbosity", Env: "DISCORD_VERBOSITY", Help: "default for how much of a turn shows in-channel, overridable per conversation with /verbosity. silent (default) shows only the answer. quiet also shows a live list of tool names. actions also shows each tool's detail. full also shows the assistant's text."},
				{Key: "context_messages", Env: "DISCORD_CONTEXT_MESSAGES", Help: "how many prior channel messages to carry as context (default 30)"},
				{Key: "playbook", Env: "DISCORD_PLAYBOOK", Help: "skill name a new session is told to follow (default pr-job)"},
				{Key: "tidy_pings", Env: "DISCORD_TIDY_PINGS", Help: "delete the message that opened a turn once its answer is posted, so a channel keeps the work and not the requests (default off; never applies where the answer lives in a thread started off the ping)"},
				{Key: "message_deletes", Env: "DISCORD_MESSAGE_DELETES", Help: "contribute the `message delete` verb, so an agent can delete messages this bot sent (default off; the verb is absent entirely until this is set, and even then it refuses any message another author wrote)"},
			},
		},
		Gateway: func(ctx context.Context, cfg contracts.PluginConfig) (contracts.GatewaySet, error) {
			return NewGatewaySet(ctx, cfg, slog.Default())
		},
		Skills: shipSkills,
	})
}

func shipSkills(context.Context, contracts.PluginConfig) (fs.FS, error) { return embeddedSkills() }

// NewGatewaySet builds the Discord channel from config: it wires the outbound
// gateway, the read/status reader, the channel admin and the reachability prober.
func NewGatewaySet(ctx context.Context, cfg contracts.PluginConfig, log *slog.Logger) (contracts.GatewaySet, error) {
	log = log.With("gateway", gatewayKind)
	run := lifetimeFrom(ctx)
	token := cfg.Get("token")
	owner := cfg.Get("owner")
	bot := dctl.New(token, "", dctl.WithGlobalCommands())
	gateway := NewGateway(discordClient{bot}, run, log)
	plat := NewPlatform(bot)
	bindPath, err := bindStorePath()
	if err != nil {
		return contracts.GatewaySet{}, err
	}
	binds := newBindStore(bindPath, log)
	plat.binds = binds

	level := verbositySetting(cfg.Get("verbosity"))
	sinks := newSinks(run, renderAdapter{plat}, func(conv string) string {
		if v := binds.Level(conv); v != "" {
			return v
		}
		return level
	}, owner, switchSetting(cfg.Get("tidy_pings"), false), log)
	gateway.sinks = sinks
	plat.sinks = sinks

	appID, err := bot.Interactions().AppID(ctx)
	if err != nil {
		return contracts.GatewaySet{}, fmt.Errorf("discord gateway: resolve application id: %w", err)
	}

	gateway.deletes = switchSetting(cfg.Get("message_deletes"), false)
	gateway.selfID = appID

	allowPath, err := allowStorePath()
	if err != nil {
		return contracts.GatewaySet{}, err
	}
	gateway.slash = newSlash(bot.Interactions(), bot.Components(), token, newAllowStore(allowPath, log), binds, log)
	address := defaultAddress(appID)
	address.requireMention = switchSetting(cfg.Get("require_mention"), address.requireMention)
	address.threadRequireMention = switchSetting(cfg.Get("thread_require_mention"), address.threadRequireMention)
	address.free = newIDSet(cfg.Get("free_response_channels"))

	scope := defaultScope()
	scope.groupPerUser = switchSetting(cfg.Get("sessions_per_user"), scope.groupPerUser)
	scope.threadPerUser = switchSetting(cfg.Get("thread_sessions_per_user"), scope.threadPerUser)

	agent := cfg.Get("agent")
	if !address.free.empty() && agent == "" {
		log.Warn("free response channels set without an agent, so replies there get no persona gating their tools")
	}

	gateway.slash.router = newRouter(
		func() contracts.SessionControl { return gateway.slash.ctrl },
		discordClient{bot}, binds, sinks, log,
		routerConfig{
			owner:           owner,
			appID:           appID,
			contextMessages: intSetting(cfg.Get("context_messages"), defaultContextMessages),
			playbook:        strSetting(cfg.Get("playbook"), "pr-job"),
			agent:           agent,
			access: accessSettings{
				users:    newIDSet(cfg.Get("allowed_users")),
				roles:    newIDSet(cfg.Get("allowed_roles")),
				channels: newIDSet(cfg.Get("allowed_channels")),
				ignored:  newIDSet(cfg.Get("ignored_channels")),
				allowAll: switchSetting(cfg.Get("allow_all_users"), false),
				bots:     botsSetting(cfg.Get("allow_bots")),
			},
			address: address,
			scope:   scope,
		},
	)
	return contracts.GatewaySet{
		Gateway: gateway,
		Reader:  plat,
		Admin:   NewChannelAdmin(bot),
		Prober:  NewProber(bot),
	}, nil
}

func stateDir() (string, error) {
	dir := os.Getenv("DCTL_STATE_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate the state directory: %w", err)
		}
		dir = filepath.Join(home, ".config", "dctl")
	}
	if err := os.MkdirAll(filepath.Clean(dir), stateDirPerm); err != nil {
		return "", fmt.Errorf("create the state directory: %w", err)
	}
	return dir, nil
}

func allowStorePath() (string, error) {
	return storePath("discord-allow.json")
}

func bindStorePath() (string, error) {
	return storePath("discord-router.json")
}

func storePath(name string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func intSetting(v string, def int) int {
	number, err := strconv.Atoi(v)
	if err != nil || number < 0 {
		return def
	}
	return number
}

func knownLevel(v string) bool {
	switch v {
	case levelSilent, levelQuiet, levelActions, levelFull:
		return true
	}
	return false
}

func verbositySetting(raw string) string {
	if knownLevel(raw) {
		return raw
	}
	return defaultLevel
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

func strSetting(value, def string) string {
	if value == "" {
		return def
	}
	return value
}
