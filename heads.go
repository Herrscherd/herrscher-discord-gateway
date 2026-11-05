package discord

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

//go:embed heads
var headFS embed.FS

type headKey string

const (
	headAck        headKey = "ack"
	headRunning    headKey = "running"
	headFail       headKey = "fail"
	headDone       headKey = "done"
	headStop       headKey = "stop"
	headThinking   headKey = "thinking"
	headToolRead   headKey = "tool.read"
	headToolEdit   headKey = "tool.edit"
	headToolSearch headKey = "tool.search"
	headToolAgent  headKey = "tool.agent"
	headToolWeb    headKey = "tool.web"
	headToolWrite  headKey = "tool.write"
	headToolOther  headKey = "tool.other"
)

const (
	headsEnv     = "DISCORD_HEADS_FILE"
	headsSetting = "heads_file"
	settingArg   = "Setting"
	valueArg     = "Value"
)

var (
	errUnknownHead    = errors.New("unknown head key")
	errNoEnglishTable = errors.New("discord gateway: head tables have no English table")
)

type heads map[i18n.Language]map[string]string

type headEntry struct {
	ID    string `json:"id"`
	Other string `json:"other"`
}

func embeddedHeads() (heads, error) {
	src, err := fs.Sub(headFS, "heads")
	if err != nil {
		return nil, fmt.Errorf("discord gateway: embedded heads: %w", err)
	}
	return loadHeads(src)
}

func loadHeads(src fs.FS) (heads, error) {
	names, err := fs.Glob(src, "*.json")
	if err != nil {
		return nil, fmt.Errorf("discord gateway: head tables: %w", err)
	}
	table := make(heads, len(names))
	for _, name := range names {
		lang, err := i18n.ParseLanguage(strings.TrimSuffix(name, ".json"))
		if err != nil {
			return nil, fmt.Errorf("discord gateway: head table %s: %w", name, err)
		}
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return nil, fmt.Errorf("discord gateway: head table %s: %w", name, err)
		}
		var entries []headEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("discord gateway: head table %s: %w", name, err)
		}
		table[lang] = make(map[string]string, len(entries))
		for _, entry := range entries {
			table[lang][entry.ID] = entry.Other
		}
	}
	if _, ok := table[i18n.English]; !ok {
		return nil, errNoEnglishTable
	}
	return table, nil
}

func (h heads) overridden(override map[string]string) (heads, error) {
	english := h[i18n.English]
	for key := range override {
		if _, known := english[key]; !known {
			return nil, fmt.Errorf("%w %q", errUnknownHead, key)
		}
	}
	next := make(heads, len(h))
	for lang, table := range h {
		copied := make(map[string]string, len(table))
		for key, head := range table {
			copied[key] = head
		}
		for key, head := range override {
			copied[key] = head
		}
		next[lang] = copied
	}
	return next, nil
}

func (m messages) withHeadsFile(path string) (messages, error) {
	if path == "" {
		return m, nil
	}
	override, err := readHeadsFile(path)
	if err != nil {
		return m, headsFileError{path: path, cause: err}
	}
	tables, err := m.heads.overridden(override)
	if err != nil {
		return m, headsFileError{path: path, cause: err}
	}
	m.heads = tables
	return m, nil
}

func readHeadsFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	var override map[string]string
	if err := json.Unmarshal(data, &override); err != nil {
		return nil, err
	}
	return override, nil
}

type headsFileError struct {
	path  string
	cause error
}

func (e headsFileError) Error() string {
	return fmt.Sprintf("discord gateway: %s %q: %v", headsEnv, e.path, e.cause)
}

func (e headsFileError) Unwrap() error { return e.cause }

func (e headsFileError) Notice() contracts.Notice {
	return contracts.Notice{Code: contracts.CodeSettingInvalid, Args: []contracts.Arg{
		contracts.Text(settingArg, headsEnv),
		contracts.Text(valueArg, e.path),
	}}
}

func noticeHead(code contracts.Code) headKey {
	return headKey(code.Key())
}

func toolHead(tool string) headKey {
	switch tool {
	case "Read":
		return headToolRead
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return headToolEdit
	case "Grep", "Glob":
		return headToolSearch
	case "Task", "Agent":
		return headToolAgent
	case "WebFetch", "WebSearch":
		return headToolWeb
	case "TodoWrite":
		return headToolWrite
	}
	return headToolOther
}
