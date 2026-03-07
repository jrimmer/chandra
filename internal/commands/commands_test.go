package commands

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Parse ─────────────────────────────────────────────────────────────────────

func TestParse_BasicCommand(t *testing.T) {
	name, args := Parse("!reset")
	assert.Equal(t, "reset", name)
	assert.Equal(t, "", args)
}

func TestParse_CommandWithArgs(t *testing.T) {
	name, args := Parse("!quiet 2h")
	assert.Equal(t, "quiet", name)
	assert.Equal(t, "2h", args)
}

func TestParse_CommandWithMultiWordArgs(t *testing.T) {
	name, args := Parse("!model anthropic/claude-haiku-4-5")
	assert.Equal(t, "model", name)
	assert.Equal(t, "anthropic/claude-haiku-4-5", args)
}

func TestParse_UpperCaseNormalized(t *testing.T) {
	name, args := Parse("!RESET")
	assert.Equal(t, "reset", name)
	assert.Equal(t, "", args)
}

func TestParse_LeadingWhitespace(t *testing.T) {
	name, args := Parse("  !status")
	assert.Equal(t, "status", name)
	assert.Equal(t, "", args)
}

func TestParse_NotACommand(t *testing.T) {
	name, _ := Parse("hello world")
	assert.Equal(t, "", name)
}

func TestParse_EmptyString(t *testing.T) {
	name, _ := Parse("")
	assert.Equal(t, "", name)
}

func TestParse_JoinExcluded(t *testing.T) {
	// !join is handled separately; Parse must return empty for it.
	name, _ := Parse("!join INVITE123")
	assert.Equal(t, "", name, "!join must not be returned by Parse — handled by its own interceptor")
}

func TestParse_BangOnly(t *testing.T) {
	name, _ := Parse("!")
	assert.Equal(t, "", name)
}

// ── Registry ──────────────────────────────────────────────────────────────────

func TestRegistry_Register_And_Lookup(t *testing.T) {
	r := &Registry{handlers: make(map[string]CommandDef)}
	r.Register("ping", CommandDef{
		Handler:     func(ctx context.Context, cmd Command, env *Env) Result { return Result{Content: "pong"} },
		Description: "Ping the bot",
		Usage:       "!ping",
		Source:      "builtin",
	})
	def, ok := r.Lookup("ping")
	require.True(t, ok)
	assert.Equal(t, "!ping", def.Usage)
}

func TestRegistry_Lookup_CaseInsensitive(t *testing.T) {
	r := &Registry{handlers: make(map[string]CommandDef)}
	r.Register("PING", CommandDef{Handler: func(_ context.Context, _ Command, _ *Env) Result { return Result{} }, Source: "builtin"})
	_, ok := r.Lookup("ping")
	assert.True(t, ok)
}

func TestRegistry_Dispatch_UnknownCommand(t *testing.T) {
	r := &Registry{handlers: make(map[string]CommandDef)}
	result, handled := r.Dispatch(context.Background(), "!unknown", Command{}, &Env{})
	require.True(t, handled, "unknown command should still be handled with an error message")
	assert.Contains(t, result.Content, "Unknown command")
	assert.Contains(t, result.Content, "!help")
}

func TestRegistry_Dispatch_NotACommand(t *testing.T) {
	r := &Registry{handlers: make(map[string]CommandDef)}
	_, handled := r.Dispatch(context.Background(), "hello world", Command{}, &Env{})
	assert.False(t, handled)
}

func TestRegistry_RegisterSkill_BuiltinWins(t *testing.T) {
	r := &Registry{handlers: make(map[string]CommandDef)}
	r.Register("status", CommandDef{Handler: func(_ context.Context, _ Command, _ *Env) Result { return Result{} }, Source: "builtin"})
	ok := r.RegisterSkill("status", "some-skill", "desc", "!status")
	assert.False(t, ok, "built-in should block skill registration of same name")
}

func TestRegistry_RegisterSkill_FirstWins(t *testing.T) {
	r := &Registry{handlers: make(map[string]CommandDef)}
	ok1 := r.RegisterSkill("weather", "skill-a", "Weather from A", "!weather")
	ok2 := r.RegisterSkill("weather", "skill-b", "Weather from B", "!weather")
	assert.True(t, ok1)
	assert.False(t, ok2, "first skill to register a command name wins")
	def, _ := r.Lookup("weather")
	assert.Equal(t, "skill-a", def.Source)
}

func TestRegistry_RemoveSkillCommands(t *testing.T) {
	r := &Registry{handlers: make(map[string]CommandDef)}
	r.RegisterSkill("weather", "weather-skill", "desc", "!weather")
	r.RegisterSkill("btc", "weather-skill", "desc", "!btc")
	r.RemoveSkillCommands("weather-skill")
	_, ok1 := r.Lookup("weather")
	_, ok2 := r.Lookup("btc")
	assert.False(t, ok1)
	assert.False(t, ok2)
}

func TestRegistry_IsSkillDelegate(t *testing.T) {
	r := &Registry{handlers: make(map[string]CommandDef)}
	r.RegisterSkill("weather", "weather-skill", "desc", "!weather")
	r.Register("status", CommandDef{Handler: func(_ context.Context, _ Command, _ *Env) Result { return Result{} }, Source: "builtin"})

	skillName, ok := r.IsSkillDelegate("weather")
	assert.True(t, ok)
	assert.Equal(t, "weather-skill", skillName)

	_, notDelegate := r.IsSkillDelegate("status")
	assert.False(t, notDelegate, "built-in with handler is not a skill delegate")
}

func TestRegistry_AllBySource_Grouping(t *testing.T) {
	r := &Registry{handlers: make(map[string]CommandDef)}
	r.Register("help", CommandDef{Handler: func(_ context.Context, _ Command, _ *Env) Result { return Result{} }, Description: "Help", Usage: "!help", Source: "builtin"})
	r.Register("status", CommandDef{Handler: func(_ context.Context, _ Command, _ *Env) Result { return Result{} }, Description: "Status", Usage: "!status", Source: "builtin"})
	r.RegisterSkill("weather", "weather-skill", "Weather", "!weather")

	builtin, bySkill := r.AllBySource()
	assert.Len(t, builtin, 2)
	assert.Len(t, bySkill["weather-skill"], 1)
}

// ── Session flags ─────────────────────────────────────────────────────────────

func openFlagsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL DEFAULT '',
		channel_id TEXT NOT NULL DEFAULT '',
		user_id TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL DEFAULT 0,
		last_active INTEGER NOT NULL DEFAULT 0,
		meta TEXT
	)`)
	require.NoError(t, err)
	return db
}

func TestSessionFlags_RoundTrip(t *testing.T) {
	db := openFlagsTestDB(t)
	_, err := db.Exec(`INSERT INTO sessions (id) VALUES ('sess-1')`)
	require.NoError(t, err)

	// Initially empty.
	model, verbose, reasoning := ReadSessionFlags(db, "sess-1")
	assert.Equal(t, "", model)
	assert.False(t, verbose)
	assert.False(t, reasoning)

	// Write flags.
	writeFlags(db, "sess-1", sessionFlags{ModelOverride: "claude-haiku", Verbose: true, Reasoning: false})

	model, verbose, reasoning = ReadSessionFlags(db, "sess-1")
	assert.Equal(t, "claude-haiku", model)
	assert.True(t, verbose)
	assert.False(t, reasoning)
}

func TestSessionFlags_UnknownSession(t *testing.T) {
	db := openFlagsTestDB(t)
	// No row exists — should return zero values, not panic.
	model, verbose, reasoning := ReadSessionFlags(db, "nonexistent")
	assert.Equal(t, "", model)
	assert.False(t, verbose)
	assert.False(t, reasoning)
}

// ── Builtin handlers ──────────────────────────────────────────────────────────

func minimalEnv(db *sql.DB) *Env {
	return &Env{
		DB:        db,
		StartedAt: time.Now().Add(-5 * time.Minute),
	}
}

func TestHandleHelp_ListsAllCommands(t *testing.T) {
	env := minimalEnv(openFlagsTestDB(t))
	r := NewRegistry(env)
	result, handled := r.Dispatch(context.Background(), "!help", Command{}, env)
	require.True(t, handled)
	assert.Contains(t, result.Content, "!reset")
	assert.Contains(t, result.Content, "!status")
	assert.Contains(t, result.Content, "!quiet")
	assert.Contains(t, result.Content, "!model")
}

func TestHandleHelp_SpecificCommand(t *testing.T) {
	env := minimalEnv(openFlagsTestDB(t))
	r := NewRegistry(env)
	result, handled := r.Dispatch(context.Background(), "!help reset", Command{}, env)
	require.True(t, handled)
	assert.Contains(t, result.Content, "!reset")
	assert.Contains(t, result.Content, "session") // description contains "Close current session"
}

func TestHandleHelp_ShowsSkillCommands(t *testing.T) {
	env := minimalEnv(openFlagsTestDB(t))
	r := NewRegistry(env)
	r.RegisterSkill("weather", "weather-skill", "Get weather", "!weather [city]")
	result, _ := r.Dispatch(context.Background(), "!help", Command{}, env)
	assert.Contains(t, result.Content, "weather-skill")
	assert.Contains(t, result.Content, "!weather")
}

func TestHandleRetry_NoLastMsg(t *testing.T) {
	env := minimalEnv(openFlagsTestDB(t))
	r := NewRegistry(env)
	result, handled := r.Dispatch(context.Background(), "!retry", Command{LastUserMsg: ""}, env)
	require.True(t, handled)
	assert.Contains(t, result.Content, "Nothing to retry")
	assert.False(t, result.Rerun)
}

func TestHandleRetry_WithLastMsg(t *testing.T) {
	env := minimalEnv(openFlagsTestDB(t))
	r := NewRegistry(env)
	result, handled := r.Dispatch(context.Background(), "!retry", Command{LastUserMsg: "tell me a joke"}, env)
	require.True(t, handled)
	assert.True(t, result.Rerun)
	assert.Equal(t, "", result.Content)
}

func TestHandleQuiet_DefaultDuration(t *testing.T) {
	db := openFlagsTestDB(t)
	// Create a heartbeat intent.
	now := time.Now().UnixMilli()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS intents (
		id TEXT PRIMARY KEY, condition TEXT, status TEXT, next_check INTEGER)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO intents VALUES ('hb-1','skill_cron:heartbeat','active',?)`, now)
	require.NoError(t, err)

	env := minimalEnv(db)
	r := NewRegistry(env)
	result, handled := r.Dispatch(context.Background(), "!quiet", Command{}, env)
	require.True(t, handled)
	assert.Contains(t, result.Content, "Heartbeat snoozed")
	assert.Contains(t, result.Content, "2h")

	// next_check should be advanced by ~2h.
	var nextCheck int64
	_ = db.QueryRow(`SELECT next_check FROM intents WHERE id='hb-1'`).Scan(&nextCheck)
	expected := now + int64(2*time.Hour/time.Millisecond)
	assert.InDelta(t, expected, nextCheck, float64(5*time.Second/time.Millisecond))
}

func TestHandleQuiet_CustomDuration(t *testing.T) {
	db := openFlagsTestDB(t)
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS intents (id TEXT PRIMARY KEY, condition TEXT, status TEXT, next_check INTEGER)`)
	require.NoError(t, err)
	now := time.Now().UnixMilli()
	_, err = db.Exec(`INSERT INTO intents VALUES ('hb-1','skill_cron:heartbeat','active',?)`, now)
	require.NoError(t, err)

	env := minimalEnv(db)
	r := NewRegistry(env)
	result, handled := r.Dispatch(context.Background(), "!quiet 30m", Command{}, env)
	require.True(t, handled)
	assert.Contains(t, result.Content, "30m")
}

func TestHandleQuiet_InvalidDuration(t *testing.T) {
	env := minimalEnv(openFlagsTestDB(t))
	r := NewRegistry(env)
	result, handled := r.Dispatch(context.Background(), "!quiet banana", Command{}, env)
	require.True(t, handled)
	assert.Contains(t, result.Content, "Couldn't parse duration")
}

func TestHandleModel_ShowCurrent(t *testing.T) {
	db := openFlagsTestDB(t)
	_, _ = db.Exec(`INSERT INTO sessions (id) VALUES ('s1')`)
	env := minimalEnv(db)
	r := NewRegistry(env)
	result, handled := r.Dispatch(context.Background(), "!model", Command{SessionID: "s1"}, env)
	require.True(t, handled)
	assert.Contains(t, result.Content, "Current model")
}

func TestHandleModel_SetOverride(t *testing.T) {
	db := openFlagsTestDB(t)
	_, _ = db.Exec(`INSERT INTO sessions (id) VALUES ('s1')`)
	env := minimalEnv(db)
	r := NewRegistry(env)
	result, handled := r.Dispatch(context.Background(), "!model anthropic/claude-haiku-4-5", Command{SessionID: "s1"}, env)
	require.True(t, handled)
	assert.Contains(t, result.Content, "anthropic/claude-haiku-4-5")

	// Verify persisted.
	model, _, _ := ReadSessionFlags(db, "s1")
	assert.Equal(t, "anthropic/claude-haiku-4-5", model)
}

func TestHandleVerbose_Toggle(t *testing.T) {
	db := openFlagsTestDB(t)
	_, _ = db.Exec(`INSERT INTO sessions (id) VALUES ('s1')`)
	env := minimalEnv(db)
	r := NewRegistry(env)

	// First toggle: off → on.
	result, handled := r.Dispatch(context.Background(), "!verbose", Command{SessionID: "s1"}, env)
	require.True(t, handled)
	assert.Contains(t, result.Content, "on")
	_, verbose, _ := ReadSessionFlags(db, "s1")
	assert.True(t, verbose)

	// Second toggle: on → off.
	result, _ = r.Dispatch(context.Background(), "!verbose", Command{SessionID: "s1"}, env)
	assert.Contains(t, result.Content, "off")
	_, verbose, _ = ReadSessionFlags(db, "s1")
	assert.False(t, verbose)
}

func TestHandleReasoning_Toggle(t *testing.T) {
	db := openFlagsTestDB(t)
	_, _ = db.Exec(`INSERT INTO sessions (id) VALUES ('s1')`)
	env := minimalEnv(db)
	r := NewRegistry(env)

	result, _ := r.Dispatch(context.Background(), "!reasoning", Command{SessionID: "s1"}, env)
	assert.Contains(t, result.Content, "on")
	_, _, reasoning := ReadSessionFlags(db, "s1")
	assert.True(t, reasoning)
}

// ── parseDuration helper ──────────────────────────────────────────────────────

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"2h", 2 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"4", 4 * time.Hour, false},   // plain integer = hours
		{"banana", 0, true},
		{"", 0, true},
	}
	for _, tc := range cases {
		d, err := parseDuration(tc.input)
		if tc.wantErr {
			assert.Error(t, err, "input=%q", tc.input)
		} else {
			require.NoError(t, err, "input=%q", tc.input)
			assert.Equal(t, tc.expected, d, "input=%q", tc.input)
		}
	}
}

// ── Skill command frontmatter parsing (in skills package) ─────────────────────
// These are integration-style checks that the parser correctly reads commands:

func TestCommandsInHelp_NoDuplicates(t *testing.T) {
	env := minimalEnv(openFlagsTestDB(t))
	r := NewRegistry(env)
	result, _ := r.Dispatch(context.Background(), "!help", Command{}, env)

	// Count occurrences of each command name — should be exactly 1.
	for _, name := range []string{"reset", "status", "quiet", "model", "verbose", "reasoning", "retry", "context", "skills", "sessions", "usage"} {
		count := strings.Count(result.Content, "!"+name)
		assert.Equal(t, 1, count, "!%s should appear exactly once in !help output", name)
	}
}

func TestAllBuiltinsHaveDescriptions(t *testing.T) {
	env := minimalEnv(openFlagsTestDB(t))
	r := NewRegistry(env)
	builtin, _ := r.AllBySource()
	for _, d := range builtin {
		assert.NotEmpty(t, d.Description, "command %q has no description", d.Usage)
		assert.NotEmpty(t, d.Usage, "command has no usage string")
	}
}

func TestDispatch_ReturnsHandledForAllBuiltins(t *testing.T) {
	db := openFlagsTestDB(t)
	_, _ = db.Exec(`INSERT INTO sessions (id) VALUES ('s1')`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS intents (id TEXT PRIMARY KEY, condition TEXT, status TEXT, next_check INTEGER)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS relationship_state (agent_id TEXT, user_id TEXT, ongoing_context TEXT)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS episodes (id TEXT, session_id TEXT, role TEXT, content TEXT, timestamp INTEGER, tags TEXT)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS sessions_full (id TEXT PRIMARY KEY, conversation_id TEXT, channel_id TEXT, user_id TEXT, started_at INTEGER, last_active INTEGER, meta TEXT)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS token_usage (id INTEGER PRIMARY KEY AUTOINCREMENT, conv_id TEXT, user_id TEXT, channel_id TEXT, model TEXT, prompt_tokens INTEGER, completion_tokens INTEGER, created_at INTEGER DEFAULT (strftime('%s','now')))`)

	env := minimalEnv(db)
	// Wire minimal config so !status doesn't panic.
	// Config left nil — handleStatus nil-guards on Config

	r := NewRegistry(env)
	// These should all return handled=true (instant commands).
	for _, input := range []string{"!help", "!retry", "!model", "!verbose", "!reasoning"} {
		cmd := Command{SessionID: "s1", ChannelID: "c1"}
		_, handled := r.Dispatch(context.Background(), input, cmd, env)
		assert.True(t, handled, "command %q should be handled", input)
	}
}
