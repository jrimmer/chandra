// Package operator provides operator-facing tools for Chandra self-management.
package operator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jrimmer/chandra/internal/config"
	"github.com/jrimmer/chandra/pkg"
)

// setConfigTool implements the set_config tool.
type setConfigTool struct {
	cfg       *config.Config
	cfgPath   string
	db        *sql.DB
	// callCtx carries the per-call channel/user context.
	// Set by SetCallContext before Execute is called.
	channelID string
	userID    string
}

// NewSetConfigTool returns a Tool that lets Chandra change her own config.
// cfg is the live config pointer (hot updates mutate it in-place).
// cfgPath is the absolute path to config.toml.
// db is an open SQLite connection (for pending_messages + config_confirmations).
func NewSetConfigTool(cfg *config.Config, cfgPath string, db *sql.DB) pkg.Tool {
	return &setConfigTool{cfg: cfg, cfgPath: cfgPath, db: db}
}

// SetCallContext injects the current message's channel/user IDs.
// Must be called before Execute for each tool invocation.
func (t *setConfigTool) SetCallContext(channelID, userID string) {
	t.channelID = channelID
	t.userID = userID
}

func (t *setConfigTool) Definition() pkg.ToolDef {
	hot, cold := config.SettableKeys()
	hotList := strings.Join(hot, ", ")
	coldList := strings.Join(cold, ", ")
	return pkg.ToolDef{
		Name: "set_config",
		Description: fmt.Sprintf(
			"Change a Chandra config setting. "+
				"Hot keys (no restart needed): %s. "+
				"Cold keys (restart + confirmation required): %s. "+
				"Use 'chandra config list' for full details.",
			hotList, coldList,
		),
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"key": {
					"type": "string",
					"description": "Dot-notation config key, e.g. \"channels.discord.require_mention\" or \"provider.default_model\"."
				},
				"value": {
					"type": "string",
					"description": "New value as a string. Booleans: \"true\"/\"false\". Integers: numeric string."
				}
			},
			"required": ["key", "value"]
		}`),
	}
}

func (t *setConfigTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(call.Parameters, &args); err != nil {
		return errResult(call.ID, pkg.ErrBadInput, "invalid parameters: "+err.Error()), nil
	}
	if args.Key == "" || args.Value == "" {
		return errResult(call.ID, pkg.ErrBadInput, "key and value are required"), nil
	}

	// 1. Validate the proposed value.
	if err := config.ValidateValue(args.Key, args.Value); err != nil {
		return errResult(call.ID, pkg.ErrBadInput, err.Error()), nil
	}

	// 2. Read current value (for rollback record).
	oldValue := t.currentValue(args.Key)

	// 3. Write updated value to config file.
	if err := updateConfigFile(t.cfgPath, args.Key, args.Value); err != nil {
		return errResult(call.ID, pkg.ErrInternal, "failed to write config: "+err.Error()), nil
	}

	// 4a. HOT key → update in-memory struct + return immediately.
	if config.IsHotKey(args.Key) {
		if err := t.applyHot(args.Key, args.Value); err != nil {
			// Non-fatal: file was already updated, in-memory update failed.
			return pkg.ToolResult{
				ID:      call.ID,
				Content: fmt.Sprintf("Updated %s = %q in config (hot). WARNING: in-memory update failed: %v — restart to apply.", args.Key, args.Value, err),
			}, nil
		}
		return pkg.ToolResult{
			ID:      call.ID,
			Content: fmt.Sprintf("Updated %s = %q (hot reload — no restart needed).", args.Key, args.Value),
		}, nil
	}

	// 4b. COLD key → Windows confirmation pattern.
	// Write pending_message and config_confirmation, then trigger restart.
	channelID := t.channelID
	userID := t.userID
	if channelID == "" {
		// Fallback: cold changes without channel context can't confirm interactively.
		return errResult(call.ID, pkg.ErrBadInput,
			"cold config changes require an active Discord conversation — run this command from #chandra-test"), nil
	}

	timeout := t.cfg.Operator.ConfigConfirmTimeoutSecs
	if timeout <= 0 {
		timeout = 30
	}
	expiresAt := time.Now().Add(time.Duration(timeout) * time.Second).Unix()

	// Backup current config before writing (already updated above — backup the pre-change content).
	bakPath := t.cfgPath + ".bak"
	// Restore original value into .bak by writing a copy of current disk state
	// (we already wrote the new value, so we need the old value back in .bak).
	// Re-read current disk (already new), then write old-value copy.
	if bakErr := backupConfigWithValue(t.cfgPath, bakPath, args.Key, oldValue); bakErr != nil {
		// Non-fatal: continue, just warn.
		fmt.Fprintf(os.Stderr, "set_config: backup failed: %v\n", bakErr)
	}

	// Write config_confirmation record.
	_, dbErr := t.db.ExecContext(ctx,
		`INSERT INTO config_confirmations (key, old_value, new_value, channel_id, user_id, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		args.Key, oldValue, args.Value, channelID, userID, expiresAt,
	)
	if dbErr != nil {
		return errResult(call.ID, pkg.ErrInternal, "failed to store confirmation: "+dbErr.Error()), nil
	}

	// Write pending_message for post-restart delivery.
	restartMsg := fmt.Sprintf("Restarted to apply config change: `%s` → `%s`. Confirming now…", args.Key, args.Value)
	_, _ = t.db.ExecContext(ctx,
		`INSERT INTO pending_messages (channel_id, content) VALUES (?, ?)`,
		channelID, restartMsg,
	)

	// Trigger restart via chandrad-config-apply (fire and forget).
	go func() {
		cmd := exec.Command("sh", "-c",
			"nohup /usr/local/bin/chandrad-config-apply >> /tmp/chandrad-config-apply.log 2>&1 &")
		_ = cmd.Run()
	}()

	return pkg.ToolResult{
		ID: call.ID,
		Content: fmt.Sprintf(
			"Config updated: `%s` = %q. Restarting now. "+
				"When I'm back up, I'll ask you to confirm — you have %ds to reply **yes** (keep) or **no** (revert). "+
				"No reply = auto-revert.",
			args.Key, args.Value, timeout,
		),
	}, nil
}

// currentValue reads the current string representation of a config key.
func (t *setConfigTool) currentValue(key string) string {
	switch key {
	case "identity.name":
		return t.cfg.Identity.Name
	case "identity.description":
		return t.cfg.Identity.Description
	case "identity.persona_file":
		return t.cfg.Identity.PersonaFile
	case "identity.max_tool_rounds":
		return strconv.Itoa(t.cfg.Identity.MaxToolRounds)
	case "provider.type":
		return t.cfg.Provider.Type
	case "provider.base_url":
		return t.cfg.Provider.BaseURL
	case "provider.api_key":
		return "***" // never log real key
	case "provider.default_model":
		return t.cfg.Provider.DefaultModel
	case "provider.embedding_model":
		return t.cfg.Provider.EmbeddingModel
	case "channels.discord.require_mention":
		if t.cfg.Channels.Discord != nil {
			return strconv.FormatBool(t.cfg.Channels.Discord.RequireMention)
		}
		return "true"
	case "channels.discord.allow_bots":
		if t.cfg.Channels.Discord != nil {
			return strconv.FormatBool(t.cfg.Channels.Discord.AllowBots)
		}
		return "false"
	case "channels.discord.reaction_status":
		if t.cfg.Channels.Discord != nil && t.cfg.Channels.Discord.ReactionStatus != nil {
			return strconv.FormatBool(*t.cfg.Channels.Discord.ReactionStatus)
		}
		return "true"
	case "channels.discord.edit_in_place":
		if t.cfg.Channels.Discord != nil {
			return strconv.FormatBool(t.cfg.Channels.Discord.EditInPlace)
		}
		return "false"
	case "operator.config_confirm_timeout_secs":
		return strconv.Itoa(t.cfg.Operator.ConfigConfirmTimeoutSecs)
	}
	return ""
}

// applyHot updates the live *config.Config struct for hot-reloadable keys.
func (t *setConfigTool) applyHot(key, value string) error {
	switch key {
	case "identity.max_tool_rounds":
		n, _ := strconv.Atoi(value)
		t.cfg.Identity.MaxToolRounds = n
	case "identity.persona_file":
		t.cfg.Identity.PersonaFile = value
	case "channels.discord.require_mention":
		if t.cfg.Channels.Discord != nil {
			t.cfg.Channels.Discord.RequireMention = value == "true"
		}
	case "channels.discord.allow_bots":
		if t.cfg.Channels.Discord != nil {
			t.cfg.Channels.Discord.AllowBots = value == "true"
		}
	case "channels.discord.reaction_status":
		if t.cfg.Channels.Discord != nil {
			b := value == "true"
			t.cfg.Channels.Discord.ReactionStatus = &b
		}
	case "channels.discord.edit_in_place":
		if t.cfg.Channels.Discord != nil {
			t.cfg.Channels.Discord.EditInPlace = value == "true"
		}
	case "operator.config_confirm_timeout_secs":
		n, _ := strconv.Atoi(value)
		if n < 15 {
			n = 15
		}
		if n > 120 {
			n = 120
		}
		t.cfg.Operator.ConfigConfirmTimeoutSecs = n
	default:
		return fmt.Errorf("no in-memory handler for hot key %q", key)
	}
	return nil
}

// updateConfigFile rewrites the TOML config file, changing the line for the
// given dotted key. Handles simple key=value lines within TOML sections.
func updateConfigFile(path, key, value string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Determine the TOML section and field name from the dotted key.
	// e.g. "channels.discord.require_mention" → section "[channels.discord]", field "require_mention"
	// e.g. "identity.max_tool_rounds" → section "[identity]", field "max_tool_rounds"
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid key format %q (expected section.field)", key)
	}
	field := parts[len(parts)-1]
	section := "[" + strings.Join(parts[:len(parts)-1], ".") + "]"

	// Format the new value for TOML: strings need quotes, booleans/ints don't.
	var formatted string
	lower := strings.ToLower(value)
	if lower == "true" || lower == "false" {
		formatted = lower
	} else if _, err2 := strconv.Atoi(value); err2 == nil {
		formatted = value
	} else {
		formatted = fmt.Sprintf("%q", value)
	}

	lines := strings.Split(string(data), "\n")
	inSection := false
	updated := false

	// Regex to match the field line (allowing spaces around =).
	fieldRe := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(field) + `\s*=`)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inSection = trimmed == section || strings.HasPrefix(trimmed, section)
		}
		if inSection && !updated && fieldRe.MatchString(line) {
			// Replace this line.
			indent := fieldRe.FindStringSubmatch(line)[1]
			lines[i] = fmt.Sprintf("%s%s = %s", indent, field, formatted)
			updated = true
		}
	}

	if !updated {
		return fmt.Errorf("key %q not found in %s (section %s, field %s)", key, path, section, field)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600)
}

// backupConfigWithValue writes a copy of the config file but with key set to oldValue.
func backupConfigWithValue(srcPath, bakPath, key, oldValue string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(bakPath, data, 0600); err != nil {
		return err
	}
	// Overwrite with old value in the backup.
	return updateConfigFile(bakPath, key, oldValue)
}

func errResult(id string, kind pkg.ToolErrorKind, msg string) pkg.ToolResult {
	return pkg.ToolResult{ID: id, Error: &pkg.ToolError{Kind: kind, Message: msg}}
}
