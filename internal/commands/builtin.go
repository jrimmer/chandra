package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// sessionFlags holds per-conversation flag overrides stored in sessions.meta.
type sessionFlags struct {
	ModelOverride string `json:"model_override,omitempty"`
	Verbose       bool   `json:"verbose,omitempty"`
	Reasoning     bool   `json:"reasoning,omitempty"`
}

// readFlags reads session flags from sessions.meta for the given session ID.
func readFlags(db *sql.DB, sessionID string) sessionFlags {
	var meta sql.NullString
	_ = db.QueryRowContext(context.Background(),
		`SELECT meta FROM sessions WHERE id = ?`, sessionID,
	).Scan(&meta)
	if !meta.Valid || meta.String == "" {
		return sessionFlags{}
	}
	var f sessionFlags
	_ = json.Unmarshal([]byte(meta.String), &f)
	return f
}

// writeFlags persists session flags to sessions.meta.
func writeFlags(db *sql.DB, sessionID string, f sessionFlags) {
	b, _ := json.Marshal(f)
	_, _ = db.ExecContext(context.Background(),
		`UPDATE sessions SET meta = ? WHERE id = ?`, string(b), sessionID,
	)
}

// ReadSessionFlags is exported for the conv worker to apply model/verbose/reasoning overrides.
func ReadSessionFlags(db *sql.DB, sessionID string) (modelOverride string, verbose, reasoning bool) {
	f := readFlags(db, sessionID)
	return f.ModelOverride, f.Verbose, f.Reasoning
}

// registerBuiltins registers all built-in instant commands into the registry.
func registerBuiltins(r *Registry, env *Env) {
	type reg struct {
		name, desc, usage string
		fn                HandlerFunc
	}
	cmds := []reg{
		{"help", "List all commands or get detail on one", "!help [command]", handleHelp(r)},
		{"reset", "Close current session — next message starts fresh", "!reset", handleReset(env)},
		{"retry", "Re-run the last message through the LLM", "!retry", handleRetry()},
		{"status", "Daemon version, uptime, active sessions, tokens today", "!status", handleStatus(env)},
		{"context", "Show ongoing context and recent episodes", "!context", handleContext(env)},
		{"skills", "List loaded skills", "!skills", handleSkills(env)},
		{"sessions", "List recent conversations", "!sessions [--limit N]", handleSessions(env)},
		{"usage", "Token usage today and all-time", "!usage", handleUsage(env)},
		{"quiet", "Snooze proactive heartbeat (default 2h)", "!quiet [duration]", handleQuiet(env)},
		{"model", "Show or set conversation model override", "!model [name]", handleModel(env)},
		{"verbose", "Toggle verbose mode (show tool calls)", "!verbose", handleVerbose(env)},
		{"reasoning", "Toggle extended thinking mode", "!reasoning", handleReasoning(env)},
	}
	for _, c := range cmds {
		r.Register(c.name, CommandDef{
			Handler:     c.fn,
			Description: c.desc,
			Usage:       c.usage,
			Source:      "builtin",
		})
	}
}

// ── !help ─────────────────────────────────────────────────────────────────────

func handleHelp(r *Registry) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		if cmd.Args != "" {
			// Detail on a specific command.
			name := strings.TrimPrefix(strings.ToLower(cmd.Args), "!")
			def, ok := r.Lookup(name)
			if !ok {
				return Result{Content: fmt.Sprintf("Unknown command `!%s`.", name)}
			}
			return Result{Content: fmt.Sprintf("**%s** — %s\nUsage: `%s`", def.Usage, def.Description, def.Usage)}
		}

		builtin, bySkill := r.AllBySource()
		var sb strings.Builder
		sb.WriteString("**Chandra commands**\n\n")
		sb.WriteString("**Built-in**\n")
		for _, d := range builtin {
			sb.WriteString(fmt.Sprintf("`%-28s` %s\n", d.Usage, d.Description))
		}
		if len(bySkill) > 0 {
			skillNames := make([]string, 0, len(bySkill))
			for k := range bySkill {
				skillNames = append(skillNames, k)
			}
			sort.Strings(skillNames)
			sb.WriteString("\n**Skill commands**\n")
			for _, sn := range skillNames {
				sb.WriteString(fmt.Sprintf("*%s:*\n", sn))
				for _, d := range bySkill[sn] {
					sb.WriteString(fmt.Sprintf("  `%-26s` %s\n", d.Usage, d.Description))
				}
			}
		}
		return Result{Content: sb.String()}
	}
}

// ── !reset ────────────────────────────────────────────────────────────────────

func handleReset(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		if err := env.Sessions.Close(cmd.SessionID); err != nil {
			return Result{Content: fmt.Sprintf("⚠️ Reset failed: %v", err)}
		}
		return Result{Content: "🔄 Session reset — I've forgotten this conversation. Next message starts fresh."}
	}
}

// ── !retry ───────────────────────────────────────────────────────────────────

func handleRetry() HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		if cmd.LastUserMsg == "" {
			return Result{Content: "Nothing to retry — no previous message found."}
		}
		return Result{Rerun: true}
	}
}

// ── !status ───────────────────────────────────────────────────────────────────

func handleStatus(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		uptime := time.Since(env.StartedAt).Round(time.Second)
		active := env.Sessions.ActiveCount()

		// Tokens today.
		todayStart := time.Now().Truncate(24 * time.Hour).Unix()
		var todayPrompt, todayCompl int
		_ = env.DB.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0)
			 FROM token_usage WHERE created_at >= ?`, todayStart,
		).Scan(&todayPrompt, &todayCompl)

		// Version from build info.
		version := "unknown"
		model := "unknown"
		if env.Config != nil {
			version = "v1" // matches const in main.go; extend later via build flags
			model = env.Config.Provider.DefaultModel
		}

		return Result{Content: fmt.Sprintf(
			"**Chandra Status**\n"+
				"Version: `%s`\n"+
				"Uptime: `%s`\n"+
				"Active sessions: `%d`\n"+
				"Tokens today: `%d` in / `%d` out\n"+
				"Model: `%s`",
			version, uptime, active, todayPrompt, todayCompl, model,
		)}
	}
}

// ── !context ──────────────────────────────────────────────────────────────────

func handleContext(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		var sb strings.Builder

		// Ongoing context from relationship_state.
		var ctxJSON sql.NullString
		_ = env.DB.QueryRowContext(ctx,
			`SELECT ongoing_context FROM relationship_state LIMIT 1`,
		).Scan(&ctxJSON)

		sb.WriteString("**Ongoing context**\n")
		if ctxJSON.Valid && ctxJSON.String != "" && ctxJSON.String != "[]" {
			var items []string
			if err := json.Unmarshal([]byte(ctxJSON.String), &items); err == nil {
				for i, item := range items {
					sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, item))
				}
			}
		} else {
			sb.WriteString("*(empty)*\n")
		}

		// Last 5 user episodes for this channel (across sessions).
		rows, err := env.DB.QueryContext(ctx,
			`SELECT e.content, e.timestamp
			 FROM episodes e
			 JOIN sessions s ON e.session_id = s.id
			 WHERE s.channel_id = ? AND e.role = 'user'
			 ORDER BY e.timestamp DESC
			 LIMIT 5`, cmd.ChannelID,
		)
		if err == nil {
			defer rows.Close()
			var episodes []string
			for rows.Next() {
				var content string
				var ts int64
				if err2 := rows.Scan(&content, &ts); err2 == nil {
					t := time.UnixMilli(ts).UTC().Format("15:04")
					excerpt := content
					if len(excerpt) > 80 {
						excerpt = excerpt[:77] + "…"
					}
					episodes = append(episodes, fmt.Sprintf("[%s] %s", t, excerpt))
				}
			}
			sb.WriteString("\n**Recent user messages**\n")
			if len(episodes) == 0 {
				sb.WriteString("*(none)*\n")
			}
			// Reverse to chronological order.
			for i := len(episodes) - 1; i >= 0; i-- {
				sb.WriteString(episodes[i] + "\n")
			}
		}

		return Result{Content: sb.String()}
	}
}

// ── !skills ───────────────────────────────────────────────────────────────────

func handleSkills(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		all := env.Skills.All()
		if len(all) == 0 {
			return Result{Content: "No skills loaded."}
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("**Skills (%d loaded)**\n", len(all)))
		for _, s := range all {
			triggers := strings.Join(s.Triggers, ", ")
			if len(triggers) > 50 {
				triggers = triggers[:47] + "…"
			}
			cronNote := ""
			if s.Cron != nil {
				cronNote = fmt.Sprintf(" ⏱ every %s", s.Cron.Interval)
			}
			sb.WriteString(fmt.Sprintf("**%s** (%s)%s\n  %s\n  Triggers: %s\n",
				s.Name, s.Category, cronNote, s.Description, triggers))
		}
		return Result{Content: sb.String()}
	}
}

// ── !sessions ─────────────────────────────────────────────────────────────────

func handleSessions(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		limit := 5
		if strings.Contains(cmd.Args, "--limit") {
			parts := strings.Fields(cmd.Args)
			for i, p := range parts {
				if p == "--limit" && i+1 < len(parts) {
					if n, err := strconv.Atoi(parts[i+1]); err == nil && n > 0 {
						limit = n
					}
				}
			}
		}
		if limit > 20 {
			limit = 20
		}

		rows, err := env.DB.QueryContext(ctx,
			`SELECT conversation_id, COUNT(*) as turns, MIN(started_at), MAX(last_active)
			 FROM sessions WHERE channel_id = ?
			 GROUP BY conversation_id ORDER BY MAX(last_active) DESC LIMIT ?`,
			cmd.ChannelID, limit,
		)
		if err != nil {
			return Result{Content: fmt.Sprintf("⚠️ Query failed: %v", err)}
		}
		defer rows.Close()

		var sb strings.Builder
		sb.WriteString("**Recent conversations**\n")
		count := 0
		for rows.Next() {
			var convID string
			var turns int
			var firstMs, lastMs int64
			if err2 := rows.Scan(&convID, &turns, &firstMs, &lastMs); err2 != nil {
				continue
			}
			first := time.UnixMilli(firstMs).UTC().Format("Jan 2 15:04")
			last := time.UnixMilli(lastMs).UTC().Format("Jan 2 15:04")
			sb.WriteString(fmt.Sprintf("`%s` — %d turns, %s → %s\n", convID, turns, first, last))
			count++
		}
		if count == 0 {
			sb.WriteString("*(none)*")
		}
		return Result{Content: sb.String()}
	}
}

// ── !usage ────────────────────────────────────────────────────────────────────

func handleUsage(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		todayStart := time.Now().Truncate(24 * time.Hour).Unix()

		var todayIn, todayOut, allIn, allOut int
		_ = env.DB.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0)
			 FROM token_usage WHERE created_at >= ?`, todayStart,
		).Scan(&todayIn, &todayOut)
		_ = env.DB.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0)
			 FROM token_usage`,
		).Scan(&allIn, &allOut)

		return Result{Content: fmt.Sprintf(
			"**Token usage**\n"+
				"Today:   `%d` in / `%d` out (`%d` total)\n"+
				"All-time: `%d` in / `%d` out (`%d` total)",
			todayIn, todayOut, todayIn+todayOut,
			allIn, allOut, allIn+allOut,
		)}
	}
}

// ── !quiet ────────────────────────────────────────────────────────────────────

func handleQuiet(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		dur := 2 * time.Hour
		if cmd.Args != "" {
			if d, err := parseDuration(cmd.Args); err == nil {
				dur = d
			} else {
				return Result{Content: fmt.Sprintf("⚠️ Couldn't parse duration `%s`. Try `2h`, `30m`, `24h`.", cmd.Args)}
			}
		}
		if dur < 5*time.Minute {
			dur = 5 * time.Minute
		}
		if dur > 48*time.Hour {
			dur = 48 * time.Hour
		}

		nextCheck := time.Now().Add(dur).UnixMilli()
		_, err := env.DB.ExecContext(ctx,
			`UPDATE intents SET next_check = ? WHERE condition = 'skill_cron:heartbeat' AND status = 'active'`,
			nextCheck,
		)
		if err != nil {
			return Result{Content: fmt.Sprintf("⚠️ Failed to snooze: %v", err)}
		}
		until := time.Now().Add(dur).UTC().Format("15:04 UTC")
		return Result{Content: fmt.Sprintf("🤫 Heartbeat snoozed for %s (until ~%s).", formatDuration(dur), until)}
	}
}

// ── !model ────────────────────────────────────────────────────────────────────

func handleModel(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		f := readFlags(env.DB, cmd.SessionID)
		if cmd.Args == "" {
			current := "unknown"
			if env.Config != nil {
				current = env.Config.Provider.DefaultModel
			}
			if f.ModelOverride != "" {
				current = f.ModelOverride + " (override)"
			}
			return Result{Content: fmt.Sprintf("Current model: `%s`\nUse `!model <name>` to override for this conversation.", current)}
		}
		f.ModelOverride = cmd.Args
		writeFlags(env.DB, cmd.SessionID, f)
		return Result{Content: fmt.Sprintf("✅ Model set to `%s` for this conversation.\nUse `!reset` to clear the override.", cmd.Args)}
	}
}

// ── !verbose ──────────────────────────────────────────────────────────────────

func handleVerbose(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		f := readFlags(env.DB, cmd.SessionID)
		f.Verbose = !f.Verbose
		writeFlags(env.DB, cmd.SessionID, f)
		if f.Verbose {
			return Result{Content: "🔊 Verbose mode **on** — tool call details will be shown."}
		}
		return Result{Content: "🔇 Verbose mode **off**."}
	}
}

// ── !reasoning ────────────────────────────────────────────────────────────────

func handleReasoning(env *Env) HandlerFunc {
	return func(ctx context.Context, cmd Command, env *Env) Result {
		f := readFlags(env.DB, cmd.SessionID)
		f.Reasoning = !f.Reasoning
		writeFlags(env.DB, cmd.SessionID, f)
		if f.Reasoning {
			return Result{Content: "🧠 Extended thinking **on** — applies if the current model supports it."}
		}
		return Result{Content: "🧠 Extended thinking **off**."}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// parseDuration handles Go duration strings plus plain numbers (treated as hours).
func parseDuration(s string) (time.Duration, error) {
	// Try standard Go duration first.
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	// Try plain integer (hours).
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Hour, nil
	}
	return 0, fmt.Errorf("unrecognized duration: %q", s)
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 && m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
