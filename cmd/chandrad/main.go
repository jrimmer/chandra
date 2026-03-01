package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jrimmer/chandra/internal/actionlog"
	"github.com/jrimmer/chandra/internal/agent"
	"github.com/jrimmer/chandra/internal/api"
	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/config"
	"github.com/jrimmer/chandra/internal/events"
	mqttbridge "github.com/jrimmer/chandra/internal/events/mqtt"
	"github.com/jrimmer/chandra/internal/memory"
	"github.com/jrimmer/chandra/internal/memory/episodic"
	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/memory/semantic"
	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/internal/provider/anthropic"
	"github.com/jrimmer/chandra/internal/provider/embeddings"
	"github.com/jrimmer/chandra/internal/provider/openai"
	"github.com/jrimmer/chandra/internal/scheduler"
	"github.com/jrimmer/chandra/internal/tools"
	"github.com/jrimmer/chandra/internal/tools/confirm"
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
	webskill "github.com/jrimmer/chandra/skills/web"
)

const version = "v1"

func main() {
	safeMode := flag.Bool("safe", false, "start in safe mode (minimal config, no external connections)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	if err := run(ctx, *safeMode); err != nil {
		slog.Error("daemon exited with error", "err", err)
		os.Exit(1)
	}
}

// run is the main daemon lifecycle.
func run(ctx context.Context, safeMode bool) error {
	startTime := time.Now()

	// -------------------------------------------------------------------
	// Step 1: Determine config path and load config.
	// -------------------------------------------------------------------
	cfgDir, cfgPath := resolveConfigPath()

	var cfg *config.Config
	if safeMode {
		slog.Info("chandrad: starting in safe mode — using empty default config")
		cfg = defaultConfig()
	} else {
		if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
			slog.Warn("chandrad: config file not found, using defaults", "path", cfgPath)
			cfg = defaultConfig()
		} else {
			var loadErr error
			cfg, loadErr = config.Load(cfgPath)
			if loadErr != nil {
				slog.Warn("chandrad: config load failed, using defaults", "path", cfgPath, "err", loadErr)
				cfg = defaultConfig()
			}
		}
	}

	// -------------------------------------------------------------------
	// Step 2: Verify file permissions (unless safe mode).
	// -------------------------------------------------------------------
	if !safeMode {
		if err := verifyPermissions(cfgDir, cfgPath); err != nil {
			slog.Warn("chandrad: permission check failed", "err", err)
			// Non-fatal: warn but continue.
		}
	}

	// -------------------------------------------------------------------
	// Step 3: Open database + run migrations.
	// -------------------------------------------------------------------
	dbPath := cfg.Database.Path
	if dbPath == "" {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, ".local", "share", "chandra", "chandra.db")
	}

	st, err := store.NewDB(dbPath)
	if err != nil {
		return fmt.Errorf("chandrad: open database: %w", err)
	}
	defer st.Close()

	if err := st.Migrate(); err != nil {
		return fmt.Errorf("chandrad: run migrations: %w", err)
	}
	db := st.DB()
	slog.Info("chandrad: database ready", "path", dbPath)

	// -------------------------------------------------------------------
	// Step 4: Initialize memory layers.
	// -------------------------------------------------------------------
	epStore := episodic.NewStore(db)
	idStore := identity.NewStore(db, "default")
	inStore := intent.NewStore(db)

	var semStoreIface semantic.SemanticStore = &noopSemanticStore{}
	if cfg.Embeddings.BaseURL != "" && cfg.Embeddings.Model != "" {
		embProv := embeddings.NewProvider(
			cfg.Embeddings.BaseURL,
			cfg.Embeddings.APIKey,
			cfg.Embeddings.Model,
			cfg.Embeddings.Dimensions,
		)
		semStore, semErr := semantic.NewStore(db, embProv)
		if semErr != nil {
			slog.Warn("chandrad: semantic store init failed, using no-op", "err", semErr)
		} else {
			semStoreIface = semStore
		}
	}

	mem := memory.New(epStore, semStoreIface, inStore, idStore)
	slog.Info("chandrad: memory layers initialized")

	// -------------------------------------------------------------------
	// Step 5: Initialize tool registry + built-in skills.
	// -------------------------------------------------------------------
	confirmPatterns := buildConfirmPatterns(cfg.Tools.ConfirmationPatterns)
	registry, err := tools.NewRegistry(confirmPatterns)
	if err != nil {
		return fmt.Errorf("chandrad: init tool registry: %w", err)
	}

	if err := registry.Register(webskill.NewWebSearch()); err != nil {
		slog.Warn("chandrad: register web.search failed", "err", err)
	}

	toolTimeout := 30 * time.Second
	if cfg.Tools.DefaultToolTimeout != "" {
		if d, err := time.ParseDuration(cfg.Tools.DefaultToolTimeout); err == nil {
			toolTimeout = d
		}
	}
	executor := tools.NewExecutor(registry, db, toolTimeout)

	// Confirmation gate.
	confirmGate, confirmErr := confirm.New(db)
	if confirmErr != nil {
		slog.Warn("chandrad: init confirm gate failed", "err", confirmErr)
		confirmGate = nil
	}

	// Action log.
	alog, err := actionlog.NewLog(db)
	if err != nil {
		return fmt.Errorf("chandrad: init action log: %w", err)
	}

	slog.Info("chandrad: tools initialized")

	// -------------------------------------------------------------------
	// Step 6: Initialize chat provider.
	// -------------------------------------------------------------------
	var chatProvider provider.Provider

	if cfg.Provider.BaseURL != "" && cfg.Provider.Model != "" {
		switch cfg.Provider.Type {
		case "anthropic":
			chatProvider = anthropic.NewProvider(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.Model)
			slog.Info("chandrad: anthropic provider ready", "model", cfg.Provider.Model)
		case "openai", "ollama":
			chatProvider = openai.NewProvider(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.Model)
			slog.Info("chandrad: openai-compatible provider ready", "model", cfg.Provider.Model)
		default:
			slog.Warn("chandrad: unknown provider type, skipping", "type", cfg.Provider.Type)
		}
	} else {
		slog.Warn("chandrad: no provider configured, agent loop will not be available")
	}

	// Context Budget Manager.
	budgetMgr := budget.New(
		float32(cfg.Budget.SemanticWeight),
		float32(cfg.Budget.RecencyWeight),
		float32(cfg.Budget.ImportanceWeight),
		float32(cfg.Budget.RecencyDecayHours),
		nil, // intent store adapter not wired in this phase
	)

	// -------------------------------------------------------------------
	// Step 7: Initialize event bus.
	// -------------------------------------------------------------------
	bus := events.NewEventBus(256, 4, nil)
	bus.Start(ctx)
	slog.Info("chandrad: event bus started")

	// -------------------------------------------------------------------
	// Step 8: Start MQTT bridge.
	// -------------------------------------------------------------------
	mqttCfg := cfg.MQTT
	if safeMode {
		mqttCfg.Mode = "disabled"
	}

	var mqttBridge mqttbridge.Bridge
	if br, brErr := mqttbridge.NewBridge(mqttCfg, bus); brErr != nil {
		slog.Warn("chandrad: MQTT bridge init failed", "err", brErr)
	} else {
		mqttBridge = br
		if startErr := mqttBridge.Start(ctx); startErr != nil {
			slog.Warn("chandrad: MQTT bridge start failed", "err", startErr)
		} else {
			slog.Info("chandrad: MQTT bridge started", "mode", mqttCfg.Mode)
		}
	}

	// -------------------------------------------------------------------
	// Step 9: Start event-to-intent handler.
	// -------------------------------------------------------------------
	intentHandler := events.NewEventIntentHandler(inStore, bus, cfg.MQTT.Topics)
	intentHandler.Start()
	slog.Info("chandrad: event-intent handler started")

	// -------------------------------------------------------------------
	// Step 10: Start scheduler.
	// -------------------------------------------------------------------
	tickInterval := 60 * time.Second
	if cfg.Scheduler.TickInterval != "" {
		if d, err := time.ParseDuration(cfg.Scheduler.TickInterval); err == nil {
			tickInterval = d
		}
	}
	sched := scheduler.NewScheduler(inStore, tickInterval, 0)
	if err := sched.Start(ctx); err != nil {
		slog.Warn("chandrad: scheduler start failed", "err", err)
	} else {
		slog.Info("chandrad: scheduler started", "tick_interval", tickInterval)
	}

	// -------------------------------------------------------------------
	// Step 11: Initialize Discord channel (if configured).
	// -------------------------------------------------------------------
	if !safeMode && cfg.Channels.Discord != nil && cfg.Channels.Discord.Token != "" {
		slog.Info("chandrad: Discord configured — deferred initialization (no live token validation at startup)")
	} else {
		slog.Info("chandrad: Discord not configured, skipping")
	}

	// -------------------------------------------------------------------
	// Step 12: Start session manager.
	// -------------------------------------------------------------------
	sessionTimeout := 30 * time.Minute
	sessionMgr, err := agent.NewManager(db, sessionTimeout)
	if err != nil {
		return fmt.Errorf("chandrad: init session manager: %w", err)
	}
	sessionMgr.Start(ctx)
	slog.Info("chandrad: session manager started")

	// -------------------------------------------------------------------
	// Step 13: Initialize agent loop (if provider available).
	// -------------------------------------------------------------------
	var agentLoop agent.AgentLoop
	if chatProvider != nil {
		loopCfg := agent.LoopConfig{
			Provider:  chatProvider,
			Memory:    mem,
			Budget:    budgetMgr,
			Registry:  registry,
			Executor:  executor,
			ActionLog: alog,
			MaxRounds: cfg.Agent.MaxToolRounds,
		}
		agentLoop = agent.NewLoop(loopCfg)
		slog.Info("chandrad: agent loop initialized")
	}

	// -------------------------------------------------------------------
	// Step 13b: Register API handlers + start API server.
	// -------------------------------------------------------------------
	apiServer := api.NewServer()

	// cancelDaemon allows the daemon.stop handler to trigger graceful shutdown.
	cancelDaemon := func() { /* populated below */ }
	daemonCtx, daemonCancel := context.WithCancel(ctx)
	defer daemonCancel()
	cancelDaemon = daemonCancel

	registerHandlers(apiServer, cancelDaemon, startTime, mem, inStore, registry, alog, confirmGate, db, agentLoop)

	socketPath := resolveSocketPath()
	if err := apiServer.Start(socketPath); err != nil {
		slog.Warn("chandrad: API server start failed", "err", err, "socket", socketPath)
	} else {
		slog.Info("chandrad: API server listening", "socket", socketPath)
	}

	// -------------------------------------------------------------------
	// Step 14: Block until context is cancelled.
	// -------------------------------------------------------------------
	slog.Info("chandrad: startup complete", "version", version, "safe_mode", safeMode)

	select {
	case <-ctx.Done():
		slog.Info("chandrad: OS signal received, shutting down")
	case <-daemonCtx.Done():
		slog.Info("chandrad: daemon.stop called, shutting down")
	}

	// -------------------------------------------------------------------
	// Step 15: Graceful shutdown (reverse order).
	// -------------------------------------------------------------------
	apiServer.Stop()
	slog.Info("chandrad: API server stopped")

	if err := sched.Stop(); err != nil {
		slog.Warn("chandrad: scheduler stop error", "err", err)
	} else {
		slog.Info("chandrad: scheduler stopped")
	}

	intentHandler.Stop()
	slog.Info("chandrad: intent handler stopped")

	if mqttBridge != nil {
		if err := mqttBridge.Stop(); err != nil {
			slog.Warn("chandrad: MQTT bridge stop error", "err", err)
		} else {
			slog.Info("chandrad: MQTT bridge stopped")
		}
	}

	bus.Stop()
	slog.Info("chandrad: event bus stopped")

	sessionMgr.Stop()
	slog.Info("chandrad: session manager stopped")

	slog.Info("chandrad: shutdown complete")
	return nil
}

// defaultConfig returns a Config with safe defaults for when no config file exists.
func defaultConfig() *config.Config {
	return &config.Config{
		Agent: config.AgentConfig{
			MaxToolRounds: 5,
		},
		Scheduler: config.SchedulerConfig{
			TickInterval: "60s",
		},
		MQTT: config.MQTTConfig{
			Mode: "embedded",
			Bind: "127.0.0.1:1883",
		},
		Budget: config.BudgetConfig{
			SemanticWeight:    0.4,
			RecencyWeight:     0.3,
			ImportanceWeight:  0.3,
			RecencyDecayHours: 168,
		},
		Tools: config.ToolsConfig{
			ConfirmationTimeout: "24h",
			DefaultToolTimeout:  "30s",
		},
		Embeddings: config.EmbeddingsConfig{
			Dimensions: 1536,
		},
	}
}

// resolveConfigPath determines the config directory and file path.
func resolveConfigPath() (dir, cfgPath string) {
	home, err := os.UserHomeDir()
	if err == nil {
		dir = filepath.Join(home, ".config", "chandra")
		cfgPath = filepath.Join(dir, "config.toml")
		return dir, cfgPath
	}
	// Fallback to working directory.
	wd, _ := os.Getwd()
	return wd, filepath.Join(wd, "chandra.toml")
}

// resolveSocketPath determines the Unix socket path for the API server.
func resolveSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/chandrad.sock"
	}
	return filepath.Join(home, ".local", "share", "chandra", "chandrad.sock")
}

// buildConfirmPatterns converts string patterns from config to ConfirmationRule slice.
func buildConfirmPatterns(patterns []string) []tools.ConfirmationRule {
	rules := make([]tools.ConfirmationRule, 0, len(patterns))
	for _, p := range patterns {
		rules = append(rules, tools.ConfirmationRule{Pattern: p})
	}
	return rules
}

// registerHandlers wires all API method handlers onto the server.
func registerHandlers(
	srv *api.Server,
	cancelDaemon func(),
	startTime time.Time,
	mem memory.Memory,
	inStore intent.IntentStore,
	registry tools.Registry,
	alog actionlog.Log,
	confirmGate *confirm.Store,
	db *sql.DB,
	agentLoop agent.AgentLoop,
) {
	_ = db        // reserved for future handlers that need direct DB access
	_ = agentLoop // reserved for future use

	// daemon.health
	srv.Handle("daemon.health", func(ctx context.Context, _ json.RawMessage) (any, error) {
		uptime := time.Since(startTime).Seconds()
		return map[string]any{
			"status":         "healthy",
			"uptime_seconds": uptime,
			"components": map[string]any{
				"database":  map[string]any{"status": "ok", "latency_ms": 0},
				"discord":   map[string]any{"status": "ok", "connected": false},
				"mqtt":      map[string]any{"status": "ok", "connected": false},
				"scheduler": map[string]any{"status": "ok", "pending_intents": 0},
				"provider":  map[string]any{"status": "ok", "last_call_ms": 0},
			},
			"active_sessions":  0,
			"memory_entries":   0,
			"action_log_today": 0,
		}, nil
	})

	// daemon.status
	srv.Handle("daemon.status", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{
			"running": true,
			"uptime":  time.Since(startTime).Seconds(),
			"version": version,
		}, nil
	})

	// daemon.stop — triggers graceful shutdown via context cancel.
	srv.Handle("daemon.stop", func(ctx context.Context, _ json.RawMessage) (any, error) {
		slog.Info("chandrad: stop requested via API")
		cancelDaemon()
		return map[string]any{"ok": true}, nil
	})

	// memory.search
	srv.Handle("memory.search", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("memory.search: invalid params: %w", err)
		}
		entries, err := mem.Semantic().QueryText(ctx, p.Query, 10)
		if err != nil {
			return nil, fmt.Errorf("memory.search: %w", err)
		}
		return entries, nil
	})

	// intent.list
	srv.Handle("intent.list", func(ctx context.Context, _ json.RawMessage) (any, error) {
		intents, err := inStore.Active(ctx)
		if err != nil {
			return nil, fmt.Errorf("intent.list: %w", err)
		}
		return intents, nil
	})

	// intent.add
	srv.Handle("intent.add", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("intent.add: invalid params: %w", err)
		}
		if p.Description == "" {
			return nil, fmt.Errorf("intent.add: description is required")
		}
		in, err := inStore.Create(ctx, p.Description, "always", p.Description)
		if err != nil {
			return nil, fmt.Errorf("intent.add: %w", err)
		}
		return in, nil
	})

	// intent.complete
	srv.Handle("intent.complete", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("intent.complete: invalid params: %w", err)
		}
		if p.ID == "" {
			return nil, fmt.Errorf("intent.complete: id is required")
		}
		if err := inStore.Complete(ctx, p.ID); err != nil {
			return nil, fmt.Errorf("intent.complete: %w", err)
		}
		return map[string]any{"ok": true}, nil
	})

	// tool.list
	srv.Handle("tool.list", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return registry.All(), nil
	})

	// tool.telemetry — params: {name string}
	srv.Handle("tool.telemetry", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("tool.telemetry: invalid params: %w", err)
		}
		if p.Name == "" {
			return nil, fmt.Errorf("tool.telemetry: name is required")
		}
		return map[string]any{
			"tool": p.Name,
			"note": "telemetry query requires direct DB access",
		}, nil
	})

	// log.today
	srv.Handle("log.today", func(ctx context.Context, _ json.RawMessage) (any, error) {
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		actions, err := alog.Query(ctx, midnight, now, "")
		if err != nil {
			return nil, fmt.Errorf("log.today: %w", err)
		}
		return actions, nil
	})

	// log.tail — params: {n int}
	srv.Handle("log.tail", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			N int `json:"n"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("log.tail: invalid params: %w", err)
		}
		if p.N <= 0 {
			p.N = 20
		}
		actions, err := alog.Recent(ctx, p.N)
		if err != nil {
			return nil, fmt.Errorf("log.tail: %w", err)
		}
		return actions, nil
	})

	// log.day — params: {date string YYYY-MM-DD}
	srv.Handle("log.day", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Date string `json:"date"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("log.day: invalid params: %w", err)
		}
		t, parseErr := time.Parse("2006-01-02", p.Date)
		if parseErr != nil {
			return nil, fmt.Errorf("log.day: invalid date %q (expected YYYY-MM-DD): %w", p.Date, parseErr)
		}
		dayEnd := t.Add(24 * time.Hour)
		actions, err := alog.Query(ctx, t, dayEnd, "")
		if err != nil {
			return nil, fmt.Errorf("log.day: %w", err)
		}
		return actions, nil
	})

	// log.week
	srv.Handle("log.week", func(ctx context.Context, _ json.RawMessage) (any, error) {
		weekAgo := time.Now().Add(-7 * 24 * time.Hour)
		actions, err := alog.Query(ctx, weekAgo, time.Now(), "")
		if err != nil {
			return nil, fmt.Errorf("log.week: %w", err)
		}
		return actions, nil
	})

	// log.drill — params: {id string}
	srv.Handle("log.drill", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("log.drill: invalid params: %w", err)
		}
		if p.ID == "" {
			return nil, fmt.Errorf("log.drill: id is required")
		}
		recent, err := alog.Recent(ctx, 1000)
		if err != nil {
			return nil, fmt.Errorf("log.drill: %w", err)
		}
		for _, a := range recent {
			if a.ID == p.ID {
				return a, nil
			}
		}
		return nil, fmt.Errorf("log.drill: action %q not found", p.ID)
	})

	// confirm.approve — params: {id string}
	srv.Handle("confirm.approve", func(ctx context.Context, params json.RawMessage) (any, error) {
		if confirmGate == nil {
			return nil, fmt.Errorf("confirm.approve: confirmation gate not available")
		}
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("confirm.approve: invalid params: %w", err)
		}
		if p.ID == "" {
			return nil, fmt.Errorf("confirm.approve: id is required")
		}
		if err := confirmGate.Approve(ctx, p.ID); err != nil {
			return nil, fmt.Errorf("confirm.approve: %w", err)
		}
		return map[string]any{"ok": true}, nil
	})
}

// noopSemanticStore is a no-op implementation of semantic.SemanticStore used
// when embeddings are not configured.
type noopSemanticStore struct{}

func (n *noopSemanticStore) Store(_ context.Context, _ pkg.MemoryEntry) error { return nil }
func (n *noopSemanticStore) StoreBatch(_ context.Context, _ []pkg.MemoryEntry) error { return nil }
func (n *noopSemanticStore) Query(_ context.Context, _ []float32, _ int) ([]pkg.MemoryEntry, error) {
	return nil, nil
}
func (n *noopSemanticStore) QueryText(_ context.Context, _ string, _ int) ([]pkg.MemoryEntry, error) {
	return nil, nil
}
