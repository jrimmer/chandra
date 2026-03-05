package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lmittmann/tint"

	"github.com/jrimmer/chandra/internal/actionlog"
	"github.com/jrimmer/chandra/internal/agent"
	"github.com/jrimmer/chandra/internal/api"
	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/channels"
	"github.com/jrimmer/chandra/internal/channels/discord"
	"github.com/jrimmer/chandra/internal/config"
	"github.com/jrimmer/chandra/internal/events"
	"github.com/jrimmer/chandra/internal/executor"
	"github.com/jrimmer/chandra/internal/planner"
	mqttbridge "github.com/jrimmer/chandra/internal/events/mqtt"
	"github.com/jrimmer/chandra/internal/infra"
	"github.com/jrimmer/chandra/internal/memory"
	"github.com/jrimmer/chandra/internal/memory/episodic"
	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/memory/semantic"
	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/internal/provider/anthropic"
	"github.com/jrimmer/chandra/internal/provider/embedcache"
	"github.com/jrimmer/chandra/internal/provider/embeddings"
	"github.com/jrimmer/chandra/internal/provider/openai"
	"github.com/jrimmer/chandra/internal/scheduler"
	"github.com/jrimmer/chandra/internal/skills"
	"github.com/jrimmer/chandra/internal/tools"
	"github.com/jrimmer/chandra/internal/tools/confirm"
	scheduletool "github.com/jrimmer/chandra/internal/tools/schedule"
	"github.com/jrimmer/chandra/pkg"
	"github.com/jrimmer/chandra/store"
	ctxtools "github.com/jrimmer/chandra/skills/context"
	webskill "github.com/jrimmer/chandra/skills/web"
)

const version = "v1"

func main() {
	safeMode := flag.Bool("safe", false, "start in safe mode (minimal config, no external connections)")
	flag.Parse()

	// G20: use tint for colorised output on TTY, JSON otherwise.
	logLevel := slog.LevelInfo
	if os.Getenv("TERM") != "" || os.Getenv("COLORTERM") != "" {
		slog.SetDefault(slog.New(tint.NewHandler(os.Stderr, &tint.Options{Level: logLevel})))
	} else {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
	}

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

	// G19: SafeWriter for config rollback watchdog (used later after API start).
	safeWriter := config.NewSafeWriter(cfgDir)

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
		// If the config directory does not exist, the user has not run
		// 'chandra init' yet — give a clear hint instead of a cryptic permission error.
		if _, statErr := os.Stat(cfgDir); errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("chandrad: no configuration found — run 'chandra init' to set up Chandra")
		}
		if err := verifyPermissions(cfgDir, cfgPath); err != nil {
			return fmt.Errorf("chandrad: permission check failed: %w (fix with: chmod 0700 %s && chmod 0600 %s)", err, cfgDir, cfgPath)
		}
		// Refuse to start if secrets.toml exists with insecure permissions.
		if err := config.CheckSecretsPermissions(cfgDir); err != nil {
			return fmt.Errorf("chandrad: startup aborted — %w", err)
		}
	}

	// -------------------------------------------------------------------
	// Step 2b: Open database + run migrations (must precede security check).
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

	// Step 2c: Security default (runs after DB is open and migrated) — deny-all: an enabled Discord channel with no
	// authorized users is a misconfiguration (design §12).
	// Policy-specific rules:
	// - "open": intentionally unrestricted, skip check (doctor will warn)
	// - "role": access via Discord roles — check allowed_roles in config
	// - all others: query the allowed_users DB table (authoritative source)
	if !safeMode && cfg.Channels.Discord != nil && cfg.Channels.Discord.BotToken != "" {
		switch cfg.Channels.Discord.AccessPolicy {
		case "open":
			// intentionally open — skip allowlist check (doctor warns)
		case "role":
			if len(cfg.Channels.Discord.AllowedRoles) == 0 {
				return fmt.Errorf("chandrad: security: access_policy=role but allowed_roles is empty — add role IDs: chandra access add discord --role <role-id>")
			}
		default: // invite, request, allowlist — DB is authoritative
			// Sum allowed users across all configured channel IDs.
			// The DB keys allowed_users by real Discord channel ID, not adapter name.
			var totalUsers int
			for _, chID := range cfg.Channels.Discord.ChannelIDs {
				n, err := countDBAllowedUsers(db, chID)
				if err != nil {
					return fmt.Errorf("chandrad: security: DB query for channel %s: %v", chID, err)
				}
				totalUsers += n
			}
			if totalUsers == 0 {
				return fmt.Errorf("chandrad: security: no authorized users in DB — the bot would lock everyone out. Run 'chandra channel test discord' or: chandra access add discord <user-id>")
			}
		}
	}

	// -------------------------------------------------------------------
	// Step 4: Initialize memory layers.
	// -------------------------------------------------------------------
	epStore := episodic.NewStore(db)
	idStore := identity.NewStore(db, "default")
	inStore := intent.NewStore(db)

	// Seed agent profile from config if none exists in DB.
	// This ensures the agent always has a name/persona even on first run.
	if _, agentErr := idStore.Agent(); agentErr != nil {
		seedProfile := identity.AgentProfile{
			Name:    cfg.Identity.Name,
			Persona: cfg.Identity.Description,
		}
		if seedErr := idStore.SetAgent(context.Background(), seedProfile); seedErr != nil {
			slog.Warn("chandrad: failed to seed agent profile from config", "err", seedErr)
		} else {
			slog.Info("chandrad: seeded agent profile from config", "name", seedProfile.Name)
		}
	}

	// Seed user_profile "default" if absent — required as FK parent for relationship_state.
	if _, userErr := idStore.User(); userErr != nil {
		seedUser := identity.UserProfile{ID: "default", Name: "User"}
		if seedErr := idStore.SetUser(context.Background(), seedUser); seedErr != nil {
			slog.Warn("chandrad: failed to seed user profile", "err", seedErr)
		} else {
			slog.Info("chandrad: seeded user profile")
		}
	}

	var semStoreIface semantic.SemanticStore = &noopSemanticStore{}
	if cfg.Embeddings.BaseURL != "" && cfg.Embeddings.Model != "" {
		embProv := embeddings.NewProvider(
			cfg.Embeddings.BaseURL,
			cfg.Embeddings.APIKey,
			cfg.Embeddings.Model,
			cfg.Embeddings.Dimensions,
		)
		// Wrap embedder with LRU cache: repeated/similar queries skip the Ollama round-trip.
		// Default: 256 entries, 5-minute TTL. Saves ~100ms per cache hit.
		cachedEmbProv := embedcache.New(embProv, embedcache.DefaultCapacity, embedcache.DefaultTTL)
		semStore, semErr := semantic.NewStore(db, cachedEmbProv)
		if semErr != nil {
			slog.Warn("chandrad: semantic store init failed, using no-op", "err", semErr)
		} else {
			semStoreIface = semStore
			slog.Info("chandrad: semantic memory enabled",
				"model", cfg.Embeddings.Model,
				"dimensions", cfg.Embeddings.Dimensions,
			)
		}
	}

	mem := memory.New(epStore, semStoreIface, inStore, idStore)
	slog.Info("chandrad: memory layers initialized")

	// -------------------------------------------------------------------
	// Step 5: Initialize tool registry + built-in skills.
	// -------------------------------------------------------------------
	confirmRules := buildConfirmRules(cfg.Tools.ConfirmationRules)
	registry, err := tools.NewRegistry(confirmRules)
	if err != nil {
		return fmt.Errorf("chandrad: init tool registry: %w", err)
	}

	if err := registry.Register(webskill.NewWebSearch()); err != nil {
		slog.Warn("chandrad: register web_search failed", "err", err)
	}
	if err := registry.Register(ctxtools.NewNoteContext(idStore)); err != nil {
		slog.Warn("chandrad: register note_context failed", "err", err)
	}
	if err := registry.Register(ctxtools.NewForgetContext(idStore)); err != nil {
		slog.Warn("chandrad: register forget_context failed", "err", err)
	}
	if err := registry.Register(scheduletool.NewScheduleReminderTool(inStore)); err != nil {
		slog.Warn("chandrad: register schedule_reminder failed", "err", err)
	}
	// skillReg is declared here and assigned in Step 5b.
	// The closure captures the variable; by the time any tool call executes,
	// skillReg will have been fully initialized.
	var skillReg *skills.Registry

	// Wire list_intents with a skill category lookup from the registry.
	skillCategoryLookup := scheduletool.SkillCategoryLookup(func(skillName string) string {
		if skillReg == nil {
			return ""
		}
		sk, ok := skillReg.Get(skillName)
		if !ok {
			return ""
		}
		return sk.Category
	})
	if err := registry.Register(scheduletool.NewListIntentsTool(inStore, skillCategoryLookup)); err != nil {
		slog.Warn("chandrad: register list_intents failed", "err", err)
	}
	if err := registry.Register(scheduletool.NewGetCurrentTimeTool()); err != nil {
		slog.Warn("chandrad: register get_current_time failed", "err", err)
	}

	// read_skill is registered after skillReg is initialized (Step 5b below).

	toolTimeout := 30 * time.Second
	if cfg.Tools.DefaultToolTimeout != "" {
		if d, err := time.ParseDuration(cfg.Tools.DefaultToolTimeout); err == nil {
			toolTimeout = d
		}
	}
	toolExec := tools.NewExecutor(registry, db, toolTimeout)

	// Confirmation gate.
	confirmGate, confirmErr := confirm.New(db)
	if confirmErr != nil {
		slog.Warn("chandrad: init confirm gate failed", "err", confirmErr)
		confirmGate = nil
	}

	// G5: wire confirm store into executor so RequiresConfirmation is enforced.
	if confirmGate != nil {
		toolExec.WithConfirmStore(confirmGate)
	}

	// Action log.
	alog, err := actionlog.NewLog(db)
	if err != nil {
		return fmt.Errorf("chandrad: init action log: %w", err)
	}

	slog.Info("chandrad: tools initialized")

	// -------------------------------------------------------------------
	// Step 5b: Initialize skill registry.
	// -------------------------------------------------------------------
	skillReg = skills.NewRegistry()

	// Wire the CronSyncer so skills with cron frontmatter auto-register intents.
	// defaultCronChannelID is the first configured Discord channel (or empty).
	defaultCronChannelID := ""
	if cfg.Channels.Discord != nil && len(cfg.Channels.Discord.ChannelIDs) > 0 {
		defaultCronChannelID = cfg.Channels.Discord.ChannelIDs[0]
	}
	defaultCronUserID := "" // admin user; intentionally empty (broadcast to channel)
	skillReg.SetCronSyncer(&skillCronSyncer{
		store:     inStore,
		channel:   defaultCronChannelID,
		userID:    defaultCronUserID,
	})

	expandedSkillDir := expandPath(cfg.Skills.Path)
	if err := skillReg.Load(ctx, expandedSkillDir, registeredToolNames(registry)); err != nil {
		slog.Warn("chandrad: skills load failed", "err", err)
	} else {
		slog.Info("chandrad: skills loaded", "loaded", len(skillReg.All()), "unmet", len(skillReg.Unmet()))
	}

	// Register the read_skill built-in tool.
	if err := registry.Register(skills.NewReadSkillTool(skillReg)); err != nil {
		slog.Warn("chandrad: register read_skill failed", "err", err)
	}

	// Register the write_skill tool so the LLM can draft new skills conversationally.
	if err := registry.Register(skills.NewWriteSkillTool(skillReg, expandedSkillDir)); err != nil {
		slog.Warn("chandrad: register write_skill failed", "err", err)
	}

	// -------------------------------------------------------------------
	// Step 5c: Initialize infrastructure manager.
	// -------------------------------------------------------------------
	infraMgr := infra.NewManager()
	if cfg.Infrastructure.MaxConcurrentHosts > 0 {
		infraMgr.MaxConcurrentHosts = cfg.Infrastructure.MaxConcurrentHosts
	}
	slog.Info("chandrad: infrastructure manager initialized")

	// -------------------------------------------------------------------
	// Step 6: Initialize chat provider.
	// -------------------------------------------------------------------
	var chatProvider provider.Provider

	if cfg.Provider.BaseURL != "" && cfg.Provider.DefaultModel != "" {
		switch cfg.Provider.Type {
		case "anthropic":
			chatProvider = anthropic.NewProvider(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.DefaultModel)
			slog.Info("chandrad: anthropic provider ready", "model", cfg.Provider.DefaultModel)
		case "openai", "ollama", "openrouter", "custom":
			chatProvider = openai.NewProvider(cfg.Provider.BaseURL, cfg.Provider.APIKey, cfg.Provider.DefaultModel)
			slog.Info("chandrad: openai-compatible provider ready", "model", cfg.Provider.DefaultModel)
		default:
			slog.Warn("chandrad: unknown provider type, skipping", "type", cfg.Provider.Type)
		}
	} else {
		slog.Warn("chandrad: no provider configured, agent loop will not be available")
	}

	// Context Budget Manager.
	// G10: wire the intent store via an adapter (budget.IntentStore uses budget.Intent,
	// while intent.IntentStore uses intent.Intent — same shape, different types).
	budgetMgr := budget.New(
		float32(cfg.Budget.SemanticWeight),
		float32(cfg.Budget.RecencyWeight),
		float32(cfg.Budget.ImportanceWeight),
		float32(cfg.Budget.RecencyDecayHours),
		&intentStoreAdapter{inStore},
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
	// Step 9: Start scheduler.
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
	// Step 10: Start event-to-intent handler.
	// -------------------------------------------------------------------
	intentHandler := events.NewEventIntentHandler(inStore, bus, cfg.MQTT.Topics)
	intentHandler.Start()
	slog.Info("chandrad: event-intent handler started")

	// -------------------------------------------------------------------
	// Step 11: Start Discord channel listener (if configured).
	//
	// dc.Listen is called here to open the websocket and begin writing to the
	// inbound channel. The processing goroutine is launched after Steps 12
	// and 13 so that sessionMgr and agentLoop are fully assigned before the
	// goroutine reads them — satisfying the Go memory model without extra sync.
	// -------------------------------------------------------------------
	var discordChannel channels.Channel
	var discordInbound chan channels.InboundMessage
	var discordDC *discord.Discord
	var discordSupervisor *channels.ChannelSupervisor
	discordConfigured := !safeMode && cfg.Channels.Discord != nil && cfg.Channels.Discord.BotToken != ""
	if discordConfigured {
		slog.Info("chandrad: starting Discord channel listener")
		dc, dcErr := discord.NewDiscord(cfg.Channels.Discord.BotToken, cfg.Channels.Discord.ChannelIDs)
		if dcErr != nil {
			return fmt.Errorf("chandrad: discord init: %w", dcErr)
		}
		// Wrap with ChannelSupervisor for exponential-backoff reconnect and health state.
		sup := channels.NewSupervisor(dc, channels.SupervisorConfig{
			InitialBackoff: time.Second,
			MaxBackoff:     30 * time.Second,
			MaxAttempts:    0, // retry forever
		})
		inbound := make(chan channels.InboundMessage, 64)
		go func() {
			if listenErr := sup.Listen(ctx, inbound); listenErr != nil && ctx.Err() == nil {
				slog.Error("chandrad: discord supervisor exited with error", "err", listenErr)
			}
		}()
		discordChannel = sup
		discordInbound = inbound
		discordDC = dc
		discordSupervisor = sup
	} else {
		slog.Info("chandrad: Discord not configured, skipping")
	}

	// -------------------------------------------------------------------
	// Step 12: Start session manager.
	// -------------------------------------------------------------------
	sessionTimeout := 30 * time.Minute
	mgr, smErr := agent.NewManager(db, sessionTimeout)
	if smErr != nil {
		return fmt.Errorf("chandrad: init session manager: %w", smErr)
	}
	mgr.Start(ctx)
	var sessionMgr agent.Manager = mgr
	slog.Info("chandrad: session manager started")

	// -------------------------------------------------------------------
	// Step 13: Initialize agent loop (if provider available).
	// -------------------------------------------------------------------
	var agentLoop agent.AgentLoop
	if chatProvider != nil {
		loopCfg := agent.LoopConfig{
			Provider:       chatProvider,
			Memory:         mem,
			Budget:         budgetMgr,
			Registry:       registry,
			Executor:       toolExec,
			ActionLog:      alog,
			Channel:        discordChannel, // G18: wire Discord channel for response sending
			Sessions:       sessionMgr,     // required for RunScheduled to process turns
			MaxRounds:      cfg.Identity.MaxToolRounds,
			SkillRegistry:  skillReg,
			SkillPriority:  cfg.Skills.Priority,
			SkillMaxTokens: cfg.Skills.MaxContextTokens,
			SkillMaxMatch:  cfg.Skills.MaxMatches,
		}
		agentLoop = agent.NewLoop(loopCfg)
		slog.Info("chandrad: agent loop initialized")
	}

	// Wire scheduler turns: consume sched.Turns() and dispatch to agentLoop.
	// This goroutine is launched after both sched and agentLoop are initialized.
	// daemonCtx is set up in Step 13b below; use a local cancel that pairs with
	// the context we derive there. We use the outer ctx here (set before daemonCtx
	// is created) and re-use the agentLoop reference captured by closure.
	// Note: daemonCtx is declared below but its cancellation is wired before
	// the select at Step 14, so using ctx here is safe for the lifetime of sched.
	if agentLoop != nil {
		go func() {
			for {
				select {
				case turn, ok := <-sched.Turns():
					if !ok {
						return
					}
					// Record the scheduled turn to the ActionLog.
					_ = alog.Record(ctx, actionlog.ActionEntry{
						Type:      actionlog.ActionScheduled,
						Summary:   fmt.Sprintf("scheduled turn for intent %s", turn.IntentID),
						SessionID: turn.SessionID,
						Details:   map[string]any{"intent_id": turn.IntentID, "prompt": turn.Prompt},
					})
					// Execute the scheduled turn. If the turn has a delivery target
					// (channel_id + user_id), send the response to that Discord channel.
					resp, schedErr := agentLoop.RunScheduled(ctx, turn)
					if schedErr != nil {
						slog.Error("scheduled turn failed", "intent", turn.IntentID, "err", schedErr)
					} else {
						// Deliver the response to the originating Discord channel.
						// "QUIET" response means the agent checked but found nothing to say.
						isQuiet := strings.TrimSpace(resp) == "QUIET"
						if resp != "" && !isQuiet && turn.ChannelID != "" && discordDC != nil {
							_ = discordDC.Send(ctx, channels.OutboundMessage{
								ChannelID: turn.ChannelID,
								Content:   resp,
							})
						}
						// Recurring vs one-shot: if the intent has a recurrence interval,
						// advance next_check instead of completing.
						if turn.RecurrenceInterval > 0 {
							nextCheck := time.Now().Add(turn.RecurrenceInterval)
							if err := inStore.Reschedule(ctx, turn.IntentID, nextCheck); err != nil {
								slog.Warn("scheduler: failed to reschedule recurring intent",
									"id", turn.IntentID, "next", nextCheck, "err", err)
							} else {
								slog.Info("scheduler: rescheduled recurring intent",
									"id", turn.IntentID, "next", nextCheck.Format(time.RFC3339))
							}
						} else {
							// One-shot: complete so it does not re-fire on every tick.
							if err := inStore.Complete(ctx, turn.IntentID); err != nil {
								slog.Warn("scheduler: failed to complete intent", "id", turn.IntentID, "err", err)
							}
						}
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Now launch the Discord dispatch goroutine.
	//
	// Dispatch model: cross-conversation parallel, within-conversation serial.
	//
	//   - Multiple conversations (different channel+user pairs) run in parallel:
	//     no conversation starves because another is slow.
	//   - Messages within a single conversation are serialized: message N+1 waits
	//     for message N to complete before the agent loop runs. This ensures:
	//       (a) episodic memory from turn N is visible when assembling context for N+1
	//       (b) responses are delivered in order
	//       (c) no concurrent writes to the same session object
	//
	// Implementation: each conversation gets a buffered channel (convQueues).
	// The router goroutine fans inbound messages into per-conversation channels.
	// Each conversation channel is drained by exactly one worker goroutine that
	// runs turns sequentially; that goroutine exits when its channel is closed
	// (triggered by ctx cancellation via convDone).
	if discordConfigured && discordDC != nil {
		type convMsg struct {
			sess *agent.Session
			msg  channels.InboundMessage
		}

		var (
			convMu    sync.Mutex
			convQueues = make(map[string]chan convMsg) // key: conversationID
		)

		// ensureWorker returns the queue channel for a conversation, creating it
		// (and its worker goroutine) on first use.
		ensureWorker := func(convID string) chan convMsg {
			convMu.Lock()
			defer convMu.Unlock()
			if q, ok := convQueues[convID]; ok {
				return q
			}
			q := make(chan convMsg, 32) // buffer up to 32 pending turns per conversation
			convQueues[convID] = q
			go func() {
				for cm := range q {
					callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
					callCtx = scheduletool.WithDelivery(callCtx, cm.msg.ChannelID, cm.msg.UserID)
					resp, runErr := agentLoop.Run(callCtx, cm.sess, cm.msg)
					cancel()
					if runErr != nil {
						slog.Error("chandrad: agent loop error",
							"conversation", convID, "err", runErr)
						continue
					}
					_ = discordDC.Send(ctx, channels.OutboundMessage{
						ChannelID: cm.msg.ChannelID,
						Content:   resp,
						ReplyToID: cm.msg.ID,
					})
				}
				convMu.Lock()
				delete(convQueues, convID)
				convMu.Unlock()
			}()
			return q
		}

		// Router goroutine: authenticates, gets/creates session, fans into per-conv queue.
		go func() {
			defer func() {
				// Drain and close all conversation queues on shutdown.
				convMu.Lock()
				for _, q := range convQueues {
					close(q)
				}
				convMu.Unlock()
			}()
			for {
				select {
				case msg, ok := <-discordInbound:
					if !ok {
						return
					}

					// Per-message access control.
					policy := cfg.Channels.Discord.AccessPolicy
					if policy == "" {
						policy = "invite"
					}
					if policy != "open" {
						var allowed bool
						_ = db.QueryRowContext(ctx,
							`SELECT COUNT(*) > 0 FROM allowed_users WHERE channel_id = ? AND user_id = ?`,
							msg.ChannelID, msg.UserID,
						).Scan(&allowed)
						if !allowed {
							slog.Warn("chandrad: discord: unauthorized user; dropping message",
								"user_id", msg.UserID, "channel_id", msg.ChannelID, "policy", policy)
							continue
						}
					}

					sess, sessErr := sessionMgr.GetOrCreate(ctx, msg.ConversationID, msg.ChannelID, msg.UserID)
					if sessErr != nil {
						slog.Error("chandrad: discord: session error", "err", sessErr)
						continue
					}
					if agentLoop == nil {
						slog.Warn("chandrad: discord: agent loop not available, dropping message")
						continue
					}

					q := ensureWorker(msg.ConversationID)
					select {
					case q <- convMsg{sess: sess, msg: msg}:
					default:
						slog.Warn("chandrad: discord: conversation queue full, dropping message",
							"conversation", msg.ConversationID)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
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

	planExec := executor.NewExecutor(alog)
	planPlan := planner.NewPlanner(chatProvider, skillReg)
	registerHandlers(apiServer, cancelDaemon, startTime, mem, inStore, registry, alog, confirmGate, db, agentLoop, sessionMgr, discordChannel, discordConfigured, skillReg, infraMgr, planExec, planPlan, cfg.Provider.BaseURL, discordSupervisor)

	socketPath := resolveSocketPath()
	if err := apiServer.Start(socketPath); err != nil {
		return fmt.Errorf("chandrad: API server start: %w", err)
	}
	slog.Info("chandrad: API server listening", "socket", socketPath)

	// G6: Expire stale confirmations every minute.
	if confirmGate != nil {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if _, err := confirmGate.ExpireStale(daemonCtx); err != nil {
						slog.Warn("confirm: expire stale failed", "err", err)
					}
				case <-daemonCtx.Done():
					return
				}
			}
		}()
	}

	// G12: Generate action log rollups every hour.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := alog.GenerateRollups(daemonCtx); err != nil {
					slog.Warn("actionlog: generate rollups failed", "err", err)
				}
			case <-daemonCtx.Done():
				return
			}
		}
	}()

	// G19: Startup health watchdog — roll back config if DB is unreachable after 10s.
	go func() {
		select {
		case <-time.After(10 * time.Second):
			if err := db.PingContext(context.Background()); err != nil {
				slog.Error("startup health check failed; rolling back config and exiting", "err", err)
				if rbErr := safeWriter.RollbackToLastGood(); rbErr != nil {
					slog.Error("config rollback failed", "err", rbErr)
				}
				os.Exit(1)
			}
		case <-daemonCtx.Done():
			return
		}
	}()

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
	// Step 15: Graceful shutdown (reverse of startup order).
	// Startup: DB(3), Memory(4), Tools(5), Provider(6), Event bus(7),
	//          MQTT(8), Scheduler(9), Event-intent handler(10), Discord(11),
	//          Session manager(12), Agent loop(13), API server(13b).
	// Shutdown reverse: API server, Session manager, Discord (ctx cancel closes it),
	//                   Event-intent handler, Scheduler, MQTT bridge, Event bus.
	// DB is closed via defer st.Close().
	// -------------------------------------------------------------------
	apiServer.Stop()
	slog.Info("chandrad: API server stopped")

	mgr.Stop()
	slog.Info("chandrad: session manager stopped")

	// Discord processing goroutine exits naturally when ctx is cancelled.
	// No explicit Stop needed.
	if discordChannel != nil {
		slog.Info("chandrad: Discord channel listener stopped (context cancelled)")
	}

	intentHandler.Stop()
	slog.Info("chandrad: intent handler stopped")

	if err := sched.Stop(); err != nil {
		slog.Warn("chandrad: scheduler stop error", "err", err)
	} else {
		slog.Info("chandrad: scheduler stopped")
	}

	if mqttBridge != nil {
		if err := mqttBridge.Stop(); err != nil {
			slog.Warn("chandrad: MQTT bridge stop error", "err", err)
		} else {
			slog.Info("chandrad: MQTT bridge stopped")
		}
	}

	bus.Stop()
	slog.Info("chandrad: event bus stopped")

	slog.Info("chandrad: shutdown complete")
	return nil
}

// defaultConfig returns a Config with safe defaults for when no config file exists.
func defaultConfig() *config.Config {
	return &config.Config{
		Identity: config.IdentityConfig{
			Name:          "Chandra",
			Description:   "A helpful personal assistant",
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
	if envPath := os.Getenv("CHANDRA_CONFIG"); envPath != "" {
		return filepath.Dir(envPath), envPath
	}
	home, err := os.UserHomeDir()
	if err == nil {
		dir = filepath.Join(home, ".config", "chandra")
		cfgPath = filepath.Join(dir, "config.toml")
		return dir, cfgPath
	}
	wd, _ := os.Getwd()
	return wd, filepath.Join(wd, "chandra.toml")
}

// resolveSocketPath determines the Unix socket path for the API server.
// It delegates to api.SocketPath() so the daemon and CLI always agree on
// the socket location.
func resolveSocketPath() string {
	return api.SocketPath()
}

// countDBAllowedUsers returns the number of rows in the allowed_users table for
// the given channel. The DB is the single authoritative source — both Hello World
// init and 'chandra access add/remove' write only to this table.
func countDBAllowedUsers(db *sql.DB, channelID string) (int, error) {
	var count int
	return count, db.QueryRow(
		"SELECT COUNT(*) FROM allowed_users WHERE channel_id = ?", channelID,
	).Scan(&count)
}

// buildConfirmRules converts ConfirmationRuleConfig entries from config to ConfirmationRule slice.
func buildConfirmRules(cfgRules []config.ConfirmationRuleConfig) []tools.ConfirmationRule {
	rules := make([]tools.ConfirmationRule, 0, len(cfgRules))
	for _, r := range cfgRules {
		rules = append(rules, tools.ConfirmationRule{
			Pattern:     r.Pattern,
			Categories:  r.Categories,
			Description: r.Description,
		})
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
	alog *actionlog.Log,
	confirmGate *confirm.Store,
	db *sql.DB,
	agentLoop agent.AgentLoop,
	sessionMgr agent.Manager,
	discordChannel channels.Channel,
	discordConfigured bool,
	skillReg *skills.Registry,
	infraMgr *infra.Manager,
	planExecutor *executor.Executor,
	planPlanner *planner.Planner,
	providerBaseURL string,
	discordSupervisor *channels.ChannelSupervisor,
) {
	// daemon.health
	srv.Handle("daemon.health", func(ctx context.Context, _ json.RawMessage) (any, error) {
		uptime := time.Since(startTime).Seconds()

		// --- Database ping ---
		dbStatus := "ok"
		dbLatencyMs := 0.0
		dbStart := time.Now()
		if pingErr := db.PingContext(ctx); pingErr != nil {
			dbStatus = "error"
			slog.Warn("chandrad: health: database ping failed", "err", pingErr)
		} else {
			dbLatencyMs = float64(time.Since(dbStart).Milliseconds())
		}

		// --- Discord status ---
		var discordInfo map[string]any
		if discordSupervisor != nil {
			state := discordSupervisor.ConnectionState()
			connected := state == channels.StateConnected
			discordInfo = map[string]any{
				"status":    state.String(),
				"connected": connected,
			}
		} else if discordConfigured {
			discordInfo = map[string]any{"status": "not_configured", "connected": false}
		} else {
			discordInfo = map[string]any{"status": "disabled", "connected": false}
		}

		// --- Provider reachability probe ---
		// Use a short-timeout TCP dial to the API base URL host.
		// This detects network outages without burning API tokens.
		providerStatus := "ok"
		{
			probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
			defer probeCancel()
			baseURL := providerBaseURL
			if baseURL == "" {
				baseURL = "https://api.openai.com"
			}
			u, parseErr := url.Parse(baseURL)
			if parseErr == nil {
				host := u.Hostname()
				port := u.Port()
				if port == "" {
					if u.Scheme == "https" {
						port = "443"
					} else {
						port = "80"
					}
				}
				var nd net.Dialer
				_, dialErr := nd.DialContext(probeCtx, "tcp", net.JoinHostPort(host, port))
				if dialErr != nil {
					providerStatus = "unreachable"
					slog.Warn("chandrad: health: provider unreachable", "err", dialErr)
				}
			}
		}

		// --- Scheduler pending intents ---
		pendingIntents := 0
		if activeIntents, intentErr := inStore.Active(ctx); intentErr == nil {
			pendingIntents = len(activeIntents)
		}

		// --- Active sessions count ---
		activeSessions := 0
		if sessionMgr != nil {
			var count int
			if rowErr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&count); rowErr == nil {
				activeSessions = count
			}
		}

		// --- Memory entries count ---
		memoryEntries := 0
		db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_entries`).Scan(&memoryEntries) //nolint:errcheck

		// --- Action log today count ---
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		actionLogToday := 0
		db.QueryRowContext(ctx, `SELECT COUNT(*) FROM action_log WHERE timestamp >= ?`, midnight.UnixMilli()).Scan(&actionLogToday) //nolint:errcheck

		// --- Overall status ---
		overallStatus := "healthy"
		if dbStatus != "ok" {
			overallStatus = "unhealthy"
		} else if providerStatus != "ok" {
			overallStatus = "degraded"
		} else if discordConfigured && discordChannel == nil {
			overallStatus = "degraded"
		}

		return map[string]any{
			"status":         overallStatus,
			"uptime_seconds": uptime,
			"components": map[string]any{
				"database":  map[string]any{"status": dbStatus, "latency_ms": dbLatencyMs},
				"discord":   discordInfo,
				"mqtt":      map[string]any{"status": "ok", "connected": false},
				"scheduler": map[string]any{"status": "ok", "pending_intents": pendingIntents},
				"provider":  map[string]any{"status": providerStatus},
			},
			"active_sessions":  activeSessions,
			"memory_entries":   memoryEntries,
			"action_log_today": actionLogToday,
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

	// daemon.start — already running if this handler is reached
	srv.Handle("daemon.start", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"ok": true, "message": "daemon already running"}, nil
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
		entries, err := mem.Semantic().QueryText(ctx, p.Query, 10, "") // "" = no user filter (admin CLI)
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
		if err := inStore.Create(ctx, intent.Intent{
			Description: p.Description,
			Condition:   "always",
			Action:      p.Description,
		}); err != nil {
			return nil, fmt.Errorf("intent.add: %w", err)
		}
		return map[string]any{"ok": true}, nil
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
		actions, err := alog.Query(ctx, midnight, now, nil)
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
		actions, err := alog.Query(ctx, t, dayEnd, nil)
		if err != nil {
			return nil, fmt.Errorf("log.day: %w", err)
		}
		return actions, nil
	})

	// log.week
	srv.Handle("log.week", func(ctx context.Context, _ json.RawMessage) (any, error) {
		weekAgo := time.Now().Add(-7 * 24 * time.Hour)
		actions, err := alog.Query(ctx, weekAgo, time.Now(), nil)
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
		entry, err := alog.GetByID(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		return entry, nil
	})

	// skill.list
	srv.Handle("skill.list", func(ctx context.Context, _ json.RawMessage) (any, error) {
		// Return full Skill structs; the client formats for display.
		return map[string]any{
			"skills": skillReg.All(),
			"unmet":  skillReg.Unmet(),
		}, nil
	})

	// skill.show — params: {name string}
	srv.Handle("skill.show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("skill.show: invalid params: %w", err)
		}
		skill, ok := skillReg.Get(req.Name)
		if !ok {
			return nil, fmt.Errorf("skill not found: %s", req.Name)
		}
		return skill, nil
	})

	// skill.reload
	srv.Handle("skill.reload", func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := skillReg.Reload(ctx); err != nil {
			return nil, err
		}
		return map[string]any{
			"reloaded": len(skillReg.All()),
			"unmet":    len(skillReg.Unmet()),
		}, nil
	})

	// skill.pending
	srv.Handle("skill.pending", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"pending": skillReg.PendingReview()}, nil
	})

	// skill.approve — params: {name string, reviewer string}
	srv.Handle("skill.approve", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			Name     string `json:"name"`
			Reviewer string `json:"reviewer"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("skill.approve: invalid params: %w", err)
		}
		if req.Reviewer == "" {
			req.Reviewer = "cli"
		}
		if err := skillReg.Approve(req.Name, req.Reviewer); err != nil {
			return nil, err
		}
		return map[string]string{"status": "approved", "skill": req.Name}, nil
	})

	// skill.reject — params: {name string, reviewer string}
	srv.Handle("skill.reject", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			Name     string `json:"name"`
			Reviewer string `json:"reviewer"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("skill.reject: invalid params: %w", err)
		}
		if req.Reviewer == "" {
			req.Reviewer = "cli"
		}
		if err := skillReg.Reject(req.Name, req.Reviewer); err != nil {
			return nil, err
		}
		return map[string]string{"status": "rejected", "skill": req.Name}, nil
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

	// plan.list — list execution plans, optionally filtered by status.
	srv.Handle("plan.list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			Status string `json:"status"`
		}
		if params != nil {
			_ = json.Unmarshal(params, &req)
		}
		plans := planExecutor.ListPlans(req.Status)
		return map[string]any{"plans": plans}, nil
	})

	// plan.show — show plan details with step status.
	srv.Handle("plan.show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("plan.show: invalid params: %w", err)
		}
		status, err := planExecutor.Status(req.ID)
		if err != nil {
			return nil, fmt.Errorf("plan.show: %w", err)
		}
		return status, nil
	})

	// plan.extend — extend a paused plan's checkpoint timeout.
	srv.Handle("plan.extend", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			ID       string `json:"id"`
			Duration string `json:"duration"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("plan.extend: invalid params: %w", err)
		}
		if err := planExecutor.ExtendCheckpoint(req.ID, req.Duration); err != nil {
			return nil, fmt.Errorf("plan.extend: %w", err)
		}
		return map[string]any{"ok": true}, nil
	})

	// plan.dry_run — decompose a goal into a plan without executing.
	srv.Handle("plan.dry_run", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			Goal string `json:"goal"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("plan.dry_run: invalid params: %w", err)
		}
		plan, err := planPlanner.Decompose(ctx, req.Goal)
		if err != nil {
			return nil, fmt.Errorf("plan.dry_run: %w", err)
		}
		return plan, nil
	})

	// plan.cancel — cancel a running or paused plan.
	srv.Handle("plan.cancel", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("plan.cancel: invalid params: %w", err)
		}
		if err := planExecutor.Cancel(req.ID); err != nil {
			return nil, fmt.Errorf("plan.cancel: %w", err)
		}
		return map[string]any{"ok": true}, nil
	})

	// plan.run — execute a plan (decompose goal then run).
	srv.Handle("plan.run", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			Goal   string `json:"goal"`
			DryRun bool   `json:"dry_run"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("plan.run: invalid params: %w", err)
		}
		plan, err := planPlanner.Decompose(ctx, req.Goal)
		if err != nil {
			return nil, fmt.Errorf("plan.run: decompose: %w", err)
		}
		if req.DryRun {
			return plan, nil
		}
		result, err := planExecutor.Run(ctx, plan)
		if err != nil {
			return nil, fmt.Errorf("plan.run: execute: %w", err)
		}
		return result, nil
	})

	// plan.resume — resume a paused plan from its checkpoint.
	srv.Handle("plan.resume", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			ID       string `json:"id"`
			Approved bool   `json:"approved"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("plan.resume: invalid params: %w", err)
		}
		result, err := planExecutor.Resume(ctx, req.ID, req.Approved)
		if err != nil {
			return nil, fmt.Errorf("plan.resume: %w", err)
		}
		return result, nil
	})

	// plan.retry — retry a failed plan from its failed step.
	srv.Handle("plan.retry", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("plan.retry: invalid params: %w", err)
		}
		result, err := planExecutor.Retry(ctx, req.ID)
		if err != nil {
			return nil, fmt.Errorf("plan.retry: %w", err)
		}
		return result, nil
	})

	// plan.rollback — rollback a failed plan's completed steps.
	srv.Handle("plan.rollback", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("plan.rollback: invalid params: %w", err)
		}
		if err := planExecutor.RollbackPlan(ctx, req.ID); err != nil {
			return nil, fmt.Errorf("plan.rollback: %w", err)
		}
		return map[string]any{"ok": true}, nil
	})

	// plan.abandon — abandon a failed plan without rollback.
	srv.Handle("plan.abandon", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("plan.abandon: invalid params: %w", err)
		}
		if err := planExecutor.Abandon(req.ID); err != nil {
			return nil, fmt.Errorf("plan.abandon: %w", err)
		}
		return map[string]any{"ok": true}, nil
	})

	// infra.list — returns all hosts and services with credentials masked.
	srv.Handle("infra.list", func(ctx context.Context, _ json.RawMessage) (any, error) {
		state := infraMgr.GetState()
		// Mask credentials in host access methods.
		for i := range state.Hosts {
			state.Hosts[i].Access.Credentials = infra.MaskCredential(state.Hosts[i].Access.Credentials)
		}
		return map[string]any{
			"hosts":    state.Hosts,
			"services": state.Services,
		}, nil
	})

	// infra.show — params: {host_id string, reveal bool}
	srv.Handle("infra.show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			HostID string `json:"host_id"`
			Reveal bool   `json:"reveal"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("infra.show: invalid params: %w", err)
		}
		host, ok := infraMgr.GetHost(req.HostID)
		if !ok {
			return nil, fmt.Errorf("infra.show: host %q not found", req.HostID)
		}
		if !req.Reveal {
			host.Access.Credentials = infra.MaskCredential(host.Access.Credentials)
		}
		status := infraMgr.HostStatus(req.HostID)
		return map[string]any{
			"host":   host,
			"status": status,
		}, nil
	})

	// infra.discover — triggers an infrastructure discovery scan.
	srv.Handle("infra.discover", func(ctx context.Context, _ json.RawMessage) (any, error) {
		err := infraMgr.Discover(ctx)
		return map[string]any{"discovered": true}, err
	})
}

// intentStoreAdapter bridges intent.IntentStore to budget.IntentStore.
// budget.IntentStore.Active returns []budget.Intent; intent.IntentStore.Active
// returns []intent.Intent — same logical shape, different package types.
type intentStoreAdapter struct {
	s intent.IntentStore
}

func (a *intentStoreAdapter) Active(ctx context.Context) ([]budget.Intent, error) {
	intents, err := a.s.Active(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]budget.Intent, len(intents))
	for i, in := range intents {
		out[i] = budget.Intent{
			ID:          in.ID,
			Description: in.Description,
			Condition:   in.Condition,
		}
	}
	return out, nil
}

// noopSemanticStore is a no-op implementation of semantic.SemanticStore used
// when embeddings are not configured.
type noopSemanticStore struct{}

func (n *noopSemanticStore) Store(_ context.Context, _ pkg.MemoryEntry) error { return nil }
func (n *noopSemanticStore) StoreBatch(_ context.Context, _ []pkg.MemoryEntry) error { return nil }
func (n *noopSemanticStore) Query(_ context.Context, _ []float32, _ int, _ string) ([]pkg.MemoryEntry, error) {
	return nil, nil
}
func (n *noopSemanticStore) QueryText(_ context.Context, _ string, _ int, _ string) ([]pkg.MemoryEntry, error) {
	return nil, nil
}

// skillCronSyncer implements skills.CronSyncer.
// It upserts/removes recurring intents for skills with cron frontmatter.
// Intents are identified by Condition = "skill_cron:<skillName>".
type skillCronSyncer struct {
	store   intent.IntentStore
	channel string // default delivery channel
	userID  string // default delivery user (empty = channel-only)
}

func (s *skillCronSyncer) UpsertSkillCron(ctx context.Context, skillName, interval, prompt, channel string) error {
	condition := "skill_cron:" + skillName

	// Check if an active intent already exists for this skill.
	active, err := s.store.Active(ctx)
	if err != nil {
		return fmt.Errorf("skill cron upsert: list active: %w", err)
	}
	for _, in := range active {
		if in.Condition == condition {
			// Already exists; nothing to do (interval/prompt changes require manual reset).
			slog.Debug("skills: cron intent already active", "skill", skillName, "intent_id", in.ID)
			return nil
		}
	}

	// Parse the interval.
	d, err := scheduletool.ParseInterval(interval)
	if err != nil {
		return fmt.Errorf("skill cron upsert: parse interval %q: %w", interval, err)
	}
	if d < time.Minute {
		return fmt.Errorf("skill cron upsert: interval must be >= 1 minute, got %v", d)
	}

	// Resolve delivery channel.
	deliveryChannel := s.channel
	if channel != "" && channel != "default" {
		deliveryChannel = channel
	}

	in := intent.Intent{
		Description:        fmt.Sprintf("Skill cron: %s", skillName),
		Condition:          condition,
		Action:             prompt,
		ChannelID:          deliveryChannel,
		UserID:             s.userID,
		NextCheck:          time.Now().Add(d), // first fire after one interval
		RecurrenceInterval: d,
	}
	if err := s.store.Create(ctx, in); err != nil {
		return fmt.Errorf("skill cron upsert: create intent: %w", err)
	}
	slog.Info("skills: cron intent created", "skill", skillName, "interval", interval)
	return nil
}

func (s *skillCronSyncer) RemoveSkillCron(ctx context.Context, skillName string) error {
	condition := "skill_cron:" + skillName
	active, err := s.store.Active(ctx)
	if err != nil {
		return fmt.Errorf("skill cron remove: list active: %w", err)
	}
	for _, in := range active {
		if in.Condition == condition {
			if err := s.store.Complete(ctx, in.ID); err != nil {
				return fmt.Errorf("skill cron remove: complete intent: %w", err)
			}
			slog.Info("skills: cron intent removed", "skill", skillName)
			return nil
		}
	}
	return nil // not found is fine
}

// expandPath expands ~ to the user's home directory.
func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// registeredToolNames returns a map of registered tool names for skill validation.
func registeredToolNames(reg tools.Registry) map[string]bool {
	defs := reg.All()
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Name] = true
	}
	return names
}
