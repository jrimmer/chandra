// Package setup implements the chandra init wizard.
package setup

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"

	"github.com/mattn/go-isatty"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/jrimmer/chandra/internal/channels/discord"
	"github.com/jrimmer/chandra/internal/config"
	"github.com/jrimmer/chandra/internal/doctor"
	"github.com/jrimmer/chandra/store"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver for direct sql.Open calls
)

// Options controls the init wizard.
type Options struct {
	// NonInteractive is reserved for future use (e.g. CI environments).
	// TODO: wire up — all huh.Form calls must check opts.NonInteractive and
	// return an error when no defaults are available rather than blocking on stdin.
	NonInteractive bool
	ConfigPath     string
	DBPath         string
}

// DefaultOptions returns sensible defaults for the wizard.
func DefaultOptions() Options {
	home, _ := os.UserHomeDir()
	return Options{
		ConfigPath: filepath.Join(home, ".config", "chandra", "config.toml"),
		DBPath:     filepath.Join(home, ".config", "chandra", "chandra.db"),
	}
}

// checkpointPath returns the path for the init checkpoint file.
func checkpointPath(cfgDir string) string {
	return filepath.Join(cfgDir, ".init-checkpoint.json")
}

// Run executes the interactive init wizard.
func Run(ctx context.Context, opts Options) error {
	// chandra init is an interactive TUI wizard — it requires a real terminal.
	// If stdin is not a TTY (e.g. piped input in CI or scripts), fail fast with
	// a clear message rather than letting huh crash with a cryptic internal error.
	if !opts.NonInteractive && !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("chandra init requires an interactive terminal (stdin is not a TTY); run 'chandra init' directly in your terminal, not via a pipe or script")
	}
	cfgDir := filepath.Dir(opts.ConfigPath)
	cpPath := checkpointPath(cfgDir)

	// Ensure the config directory exists before the first SaveCheckpoint call.
	// writeConfig also calls MkdirAll, but SaveCheckpoint in Stage 1 runs first,
	// so on a fresh machine with no ~/.config/chandra/ the checkpoint write would
	// fail with ENOENT without this early creation.
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Reset package-level customBaseURL so stale state from a previous run in the
	// same process (e.g. in tests) cannot bleed through to writeConfig.
	customBaseURL = ""

	// reconfigureHints is populated when the user picks "Reconfigure" for an
	// existing config. It is applied to cp AFTER the checkpoint-load/resume block
	// so that the checkpoint load cannot overwrite these pre-filled defaults.
	var reconfigureHints *Checkpoint

	// isFreshStart is set when the user picks "Fresh start" or "Start over".
	// Stored in cp.FreshStart so a resumed run can restore freshArchivePath.
	var isFreshStart bool

	// freshArchivePath is set when the user picks "Fresh start" or "Start over".
	// The old config is renamed to this path just before writeConfig in Stage 4 —
	// not immediately — so that an abort mid-wizard doesn't leave the existing
	// installation broken. It is also persisted in cp.FreshStart so that a resumed
	// session still archives the config even after a crash between Stage 1 and 4.
	var freshArchivePath string

	// Check for existing config. Treat only "not found" as absence; other errors
	// (permission denied, bad mount) are surfaced immediately rather than
	// silently falling through to fresh-start, which could overwrite a valid
	// but temporarily inaccessible config.
	_, configStatErr := os.Stat(opts.ConfigPath)
	if configStatErr != nil && !os.IsNotExist(configStatErr) {
		return fmt.Errorf("check config path %s: %w", opts.ConfigPath, configStatErr)
	}
	configExists := configStatErr == nil

	// Load the checkpoint now — before the existing-config prompt — so that
	// checkpointPending reflects whether there is a VALID checkpoint, not just
	// whether a (possibly corrupt) checkpoint file exists. A corrupt file would
	// otherwise suppress the reconfigure/fresh/cancel prompt and silently archive
	// the existing config as if a fresh start were intended.
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		// Corrupt checkpoint (truncated JSON from a mid-write crash) — warn and
		// treat as no checkpoint rather than aborting, which would brick 'chandra init'.
		fmt.Printf("Warning: Checkpoint file is corrupt (%v); treating as no checkpoint.\n", err)
		_ = os.Remove(cpPath)
		cp = nil
	}
	checkpointPending := cp != nil

	if configExists && !checkpointPending {
		// Config exists with no pending checkpoint — prompt for reconfigure/fresh/cancel.
		// (When a checkpoint IS pending, we skip this and go straight to the resume
		// prompt below, so the user can resume their in-progress setup.)
		var choice string
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("Configuration already exists at %s", opts.ConfigPath)).
				Options(
					huh.NewOption("Update credentials (re-enter API key and bot token; config regenerated from wizard values, other settings reset to defaults)", "reconfigure"),
					huh.NewOption("Fresh start (archive and replace existing config)", "fresh"),
					huh.NewOption("Cancel", "cancel"),
				).
				Value(&choice),
		))
		if err := form.Run(); err != nil {
			return err
		}
		switch choice {
		case "cancel":
			return nil
		case "fresh":
			// Mark for deferred archive: the actual rename is deferred to Stage 4
			// (just before writeConfig) so that an abort mid-wizard doesn't leave
			// the installation broken with the config renamed away but no new config
			// written yet. isFreshStart is stored in cp.FreshStart when the first
			// checkpoint is saved, so a resumed run still archives the old config.
			freshArchivePath = opts.ConfigPath + ".bak"
			isFreshStart = true
			_ = DeleteCheckpoint(cpPath)
		case "reconfigure":
			// Delete any dangling checkpoint — reconfigure always starts fresh from the
			// existing config file, never from a previous partial wizard run.
			_ = DeleteCheckpoint(cpPath)
			// Build hints from non-secret values in the existing config. Secrets
			// (API key, bot token) are always re-collected before the config is rewritten.
			// ConfigWritten = false so Stage 4 re-runs after secrets are re-collected.
			// NOTE: writeConfig regenerates the config from scratch — wizard-managed fields
			// are preserved from hints but other settings (budget weights, scheduler
			// interval, etc.) revert to defaults. Also, the wizard only collects one
			// Discord channel ID — extra channels in the existing config are lost.
			// We take a backup here (same as Fresh start) to preserve the original.
			freshArchivePath = opts.ConfigPath + ".bak"
			isFreshStart = true
			loadedCfg, loadErr := config.Load(opts.ConfigPath)
			if loadErr != nil {
				// Hard-fail: if we can't read the existing config, we must not silently
				// proceed as a fresh setup (that would overwrite the user's config with
				// empty wizard state). Offer to run fresh start explicitly instead.
				return fmt.Errorf("reconfigure: cannot read existing config: %w\n"+
					"Run 'chandra init' again and choose \"Fresh start\" to replace it.", loadErr)
			}
			hints := &Checkpoint{
				ProviderDone:     true,
				ProviderType:     loadedCfg.Provider.Type,
				ProviderModel:    loadedCfg.Provider.DefaultModel,
				ProviderBaseURL:  loadedCfg.Provider.BaseURL,
				ChannelsDone:     true,
				IdentityDone:     true,
				AgentName:        loadedCfg.Identity.Name,        // Phase 0 renamed Agent→Identity
				AgentDescription: loadedCfg.Identity.Description, // Phase 0 renamed Persona→Description
			}
			if loadedCfg.Channels.Discord != nil {
				if len(loadedCfg.Channels.Discord.ChannelIDs) > 0 {
					// The wizard only configures one channel ID; preserve the first.
					hints.DiscordChannelID = loadedCfg.Channels.Discord.ChannelIDs[0]
				}
				hints.AccessPolicy = loadedCfg.Channels.Discord.AccessPolicy
				hints.DiscordRoleIDs = loadedCfg.Channels.Discord.AllowedRoles
			}
			reconfigureHints = hints
			// Propagate FreshStart intent and pre-save the checkpoint immediately.
			// In reconfigure mode all stages are Done=true, so Stages 1-3 are skipped
			// and the archive happens right before writeConfig in Stage 4. Without this
			// pre-save there is no checkpoint to resume from if writeConfig fails after
			// the archive has already renamed the live config aside.
			reconfigureHints.FreshStart = isFreshStart
			// Fatal: all three stages will be skipped (Done=true), so the archive
			// in Stage 4 runs without any subsequent checkpoint save. If writeConfig
			// or DB init then fails, the live config is stranded in .bak with no
			// checkpoint to resume from. Fail here before entering that window.
			if saveErr := SaveCheckpoint(cpPath, reconfigureHints); saveErr != nil {
				return fmt.Errorf("reconfigure: pre-save checkpoint: %w", saveErr)
			}
		}
	}

	if cp != nil {
		var choice string
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Previous setup session found.").
				Description(checkpointSummary(cp)).
				Options(
					huh.NewOption("Resume from where you left off", "resume"),
					huh.NewOption("Start over", "restart"),
					huh.NewOption("Cancel", "cancel"),
				).
				Value(&choice),
		))
		if err := form.Run(); err != nil {
			return err
		}
		switch choice {
		case "cancel":
			return nil
		case "restart":
			// Delete the persisted checkpoint file immediately — otherwise a crash
			// or cancel before the next SaveCheckpoint would still offer to resume.
			_ = DeleteCheckpoint(cpPath)
			cp = &Checkpoint{}
			// If a config exists, archive it at Stage 4 just like "Fresh start" —
			// the user is explicitly abandoning it, so don't silently overwrite it.
			if configExists {
				freshArchivePath = opts.ConfigPath + ".bak"
				isFreshStart = true
			}
		default:
			// "resume" — restore freshArchivePath from the checkpoint so the resumed
			// run still archives the old config if the user originally picked "Fresh start".
			if cp.FreshStart && configExists {
				freshArchivePath = opts.ConfigPath + ".bak"
				isFreshStart = true
			} else if cp.FreshStart && !configExists {
				// Archive already happened (config.toml renamed to .bak) but the
				// FreshStart=false checkpoint save was skipped (non-fatal path) or
				// the process crashed in that tiny window. Clear the stale flag so
				// a subsequent resume does not attempt a second archive.
				cp.FreshStart = false
				if err := SaveCheckpoint(cpPath, cp); err != nil {
					slog.Warn("init: failed to clear stale FreshStart on resume (non-fatal)", "err", err)
				}
			}
		}
	} else {
		cp = &Checkpoint{}
		fmt.Println("Welcome to Chandra! Let's get you set up.")
		fmt.Println()
	}

	// Apply reconfigure hints if the user chose "Reconfigure" above. This must
	// happen after the checkpoint-load/resume block so the checkpoint load cannot
	// overwrite the pre-filled defaults.
	if reconfigureHints != nil {
		cp = reconfigureHints
	}

	// Pre-Stage-1 guard: if the checkpoint has a custom provider but an invalid
	// or missing base URL, clear ProviderDone so Stage 1 re-runs and the user
	// can supply a correct HTTPS URL via the normal form. Hard-failing here
	// would create an unrecoverable resume loop (ProviderDone stays true →
	// error → resume → same error). Uses url.Parse to reject structurally
	// invalid HTTPS URLs (e.g. "https://", "https:///v1") that pass a simple
	// prefix check but fail later in the provider check.
	if cp.ProviderType == "custom" {
		u, parseErr := url.Parse(cp.ProviderBaseURL)
		if cp.ProviderBaseURL == "" || parseErr != nil || u.Scheme != "https" || u.Host == "" {
			fmt.Printf("Warning: Custom provider URL %q is missing or invalid (must be https://host/...); re-running provider setup.\n", cp.ProviderBaseURL)
			cp.ProviderDone = false
			cp.ProviderBaseURL = ""
			customBaseURL = ""
			if err := SaveCheckpoint(cpPath, cp); err != nil {
				return err
			}
		}
	}

	// --- Stage 1: Provider ---
	var providerType, apiKey, model string
	if !cp.ProviderDone {
		if err := runProviderStage(ctx, &providerType, &apiKey, &model); err != nil {
			return err
		}
		cp.ProviderDone = true
		cp.ProviderType = providerType
		cp.ProviderModel = model
		cp.ProviderBaseURL = customBaseURL // empty string unless "custom" provider
		cp.FreshStart = isFreshStart       // persist so a resumed run still archives old config
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	} else {
		providerType = cp.ProviderType
		model = cp.ProviderModel
		customBaseURL = cp.ProviderBaseURL // restore custom URL before writeConfig runs
		// Note: HTTPS enforcement for custom URLs is done in the Pre-Stage-1 guard
		// above — by this point any non-HTTPS custom URL has already been cleared
		// and ProviderDone reset, so we never reach this else-branch with a bad URL.
		// apiKey is a secret — not stored in checkpoint. Always re-collect on resume:
		// the user may need to fix a bad key if the provider doctor check failed after
		// Stage 4. (Consistent with discordToken re-prompt in Stage 2 else branch.)
		form := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title(fmt.Sprintf("Re-enter your %s API key", providerType)).
				EchoMode(huh.EchoModePassword).
				Value(&apiKey),
		))
		if err := form.Run(); err != nil {
			return err
		}
		// Secrets were re-collected; mark Stage 4 to re-run so the corrected
		// credentials are written to disk, even if a prior run already wrote a
		// config. Without this, a user who resumes after a failed provider check
		// re-enters their key but Stage 4 is skipped and the stale key remains
		// on disk.
		if cp.ConfigWritten {
			cp.ConfigWritten = false
			if err := SaveCheckpoint(cpPath, cp); err != nil {
				return err
			}
		}
		fmt.Printf("Provider: %s / %s (from checkpoint)\n", providerType, model)
	}

	// Pre-Stage-2 guard: if access_policy=role but no role IDs are saved, the
	// channel config imported from reconfigure is incomplete. Clear ChannelsDone
	// so Stage 2 re-runs and the user can supply the missing role IDs.
	// Without this, AllowlistCheck would fail every resume with no way to repair.
	if cp.ChannelsDone && cp.AccessPolicy == "role" && len(cp.DiscordRoleIDs) == 0 {
		fmt.Println("Warning: Access policy 'role' requires at least one role ID; re-running channel setup.")
		cp.ChannelsDone = false
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	}

	// --- Stage 2: Channels ---
	var discordToken, discordChannelID, accessPolicy string
	var discordRoleIDs []string
	// Semantic memory stage outputs.
	var embBaseURL, embModel string
	var embDimensions int
	if !cp.ChannelsDone {
		if err := runChannelStage(ctx, &discordToken, &discordChannelID, &accessPolicy, &discordRoleIDs); err != nil {
			return err
		}
		cp.ChannelsDone = true
		cp.DiscordChannelID = discordChannelID
		cp.AccessPolicy = accessPolicy
		cp.DiscordRoleIDs = discordRoleIDs
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	} else {
		// Restore non-secret channel values from checkpoint.
		discordChannelID = cp.DiscordChannelID
		accessPolicy = cp.AccessPolicy
		discordRoleIDs = cp.DiscordRoleIDs
		// discordToken is a secret — not stored in checkpoint. Always re-collect on
		// resume: Stage 5 (Hello World) needs it regardless of whether ConfigWritten
		// is set, and skipping it would prevent AllowlistCheck from re-running on
		// a resumed session where it previously failed (data-loss bypass).
		if discordChannelID != "" {
			form := huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Re-enter your Discord bot token to complete setup").
					EchoMode(huh.EchoModePassword).
					Value(&discordToken),
			))
			if err := form.Run(); err != nil {
				return err
			}
		}
		// Defensive reset: if ConfigWritten is still true (e.g. Stage 1 else-branch
		// was not reached for some reason), ensure Stage 4 re-runs so the
		// newly collected token is written to disk.
		if cp.ConfigWritten {
			cp.ConfigWritten = false
			if err := SaveCheckpoint(cpPath, cp); err != nil {
				return err
			}
		}
	}

	// --- Stage 3: Identity ---
	var agentName, agentDescription string
	agentName = "Chandra"
	if !cp.IdentityDone {
		form := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("Give your assistant a name").Value(&agentName).Placeholder("Chandra"),
			huh.NewInput().Title("Brief personality description (optional)").Value(&agentDescription),
		))
		if err := form.Run(); err != nil {
			return err
		}
		if agentName == "" {
			agentName = "Chandra"
		}
		cp.IdentityDone = true
		cp.AgentName = agentName
		cp.AgentDescription = agentDescription
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	} else {
		agentName = cp.AgentName
		if agentName == "" {
			agentName = "Chandra"
		}
		agentDescription = cp.AgentDescription
	}

	// --- Stage 3b: Semantic memory (optional Ollama) ---
	if !cp.ConfigWritten {
		var semErr error
		embBaseURL, embModel, embDimensions, semErr = runSemanticStage(ctx)
		if semErr != nil {
			fmt.Printf("\nWarning: semantic memory setup: %v — continuing without it.\n", semErr)
			embBaseURL, embModel = "", ""
		}
	}

	// --- Stage 4: Write config + initialise database ---
	if !cp.ConfigWritten {
		fmt.Println()

		// If the user chose "Fresh start", archive the existing config now —
		// just before overwriting it. Doing this here (not earlier) ensures the
		// old config is intact if the wizard aborts before this point.
		// If a .bak already exists, use a timestamped suffix to avoid silently
		// discarding the previous backup.
		if freshArchivePath != "" {
			candidate := freshArchivePath
			if _, statErr := os.Stat(candidate); statErr == nil {
				// .bak already exists; find a unique timestamped name.
				// Loop with numeric suffix so two archives in the same second
				// don't silently overwrite each other.
				ts := time.Now().Format("20060102-150405")
				for i := 0; ; i++ {
					if i == 0 {
						candidate = opts.ConfigPath + "." + ts + ".bak"
					} else {
						candidate = opts.ConfigPath + fmt.Sprintf(".%s-%d.bak", ts, i)
					}
					if _, statErr := os.Stat(candidate); os.IsNotExist(statErr) {
						freshArchivePath = candidate
						break
					}
				}
			}
			if err := os.Rename(opts.ConfigPath, freshArchivePath); err != nil {
				return fmt.Errorf("archive existing config: %w", err)
			}
			fmt.Printf("Existing config archived to %s\n", freshArchivePath)
			// Archive done. Clear FreshStart in the checkpoint so that a later
			// resume (after a Stage-4/5/6 failure) does not try to archive again.
			// Without this, a resumed run would re-archive the freshly written
			// config.toml into another .bak, leaving no live config on disk if
			// writeConfig then fails.
			cp.FreshStart = false
			freshArchivePath = ""
			isFreshStart = false
			// Non-fatal: if this checkpoint save fails, the config write must
			// still proceed (the archive already happened — returning here would
			// leave the install with no live config). On a subsequent resume the
			// P2 guard below (configExists=false + cp.FreshStart=true) detects
			// that the archive already completed and clears the flag.
			if err := SaveCheckpoint(cpPath, cp); err != nil {
				slog.Warn("init: failed to clear FreshStart in checkpoint after archive (non-fatal)", "err", err)
			}
		}

		// Write embeddings section if semantic memory was enabled.
		if embBaseURL != "" {
			content, readErr := os.ReadFile(opts.ConfigPath)
			if readErr == nil {
				embSection := fmt.Sprintf(`
[embeddings]
base_url   = %q
api_key    = ""
model      = %q
dimensions = %d
`, embBaseURL, embModel, embDimensions)
				_ = os.WriteFile(opts.ConfigPath, append(content, []byte(embSection)...), 0600)
			}
		}

		fmt.Print("[Step 1/6] Writing config...          ")
		if err := writeConfig(opts.ConfigPath, providerType, apiKey, model, agentName, agentDescription, discordToken, discordChannelID, accessPolicy, discordRoleIDs, opts.DBPath); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		fmt.Println("done")

		fmt.Print("[Step 2/6] Setting permissions...     ")
		if err := os.Chmod(cfgDir, 0700); err != nil {
			fmt.Printf("warning: %v\n", err)
		} else if err := os.Chmod(opts.ConfigPath, 0600); err != nil {
			fmt.Printf("warning: %v\n", err)
		} else {
			fmt.Println("done (config: 0600, dir: 0700)")
		}

		fmt.Print("[Step 3/6] Initialising database...   ")
		dbStore, err := store.NewDB(opts.DBPath)
		if err != nil {
			fmt.Printf("failed: %v\n", err)
			return fmt.Errorf("initialise database: %w", err)
		}
		fmt.Println("done")
		fmt.Print("[Step 4/6] Running migrations...      ")
		if err := dbStore.Migrate(); err != nil {
			dbStore.Close()
			fmt.Printf("failed: %v\n", err)
			return fmt.Errorf("run migrations: %w", err)
		}
		dbStore.Close()
		fmt.Println("done")

		cp.ConfigWritten = true
		if err := SaveCheckpoint(cpPath, cp); err != nil {
			return err
		}
	}

	// --- Stage 5: Hello World loop (if Discord configured) ---
	var loopSucceeded bool
	if discordToken != "" && discordChannelID != "" {
		fmt.Println("[Step 5/6] Starting listener...       done")
		fmt.Print("[Step 6/6] Sending verification...    ")
		result, loopErr := discord.RunLoopTest(ctx, discordToken, discordChannelID, discord.DefaultVerifyOptions())
		if loopErr != nil {
			fmt.Printf("failed\nWarning: No reply received. Config saved, but channel loop is unverified.\n")
			fmt.Printf("  Run 'chandra channel test discord' when ready to complete verification.\n")
		} else {
			fmt.Println("done")
			fmt.Printf("\nReply received from %s (id: %s)\n", result.ReplyUsername, result.ReplyUserID)
			fmt.Println("  Full loop confirmed — inbound and outbound working.")
			loopSucceeded = true

			// Bootstrap DB: channel_verifications + allowed_users.
			// Use the real Discord channel ID as the key — NOT the adapter name "discord".
			if err := persistChannelVerified(opts.DBPath, discordChannelID, result.ReplyUserID); err != nil {
				fmt.Printf("  Warning: could not save channel verification: %v\n", err)
				fmt.Println("  Run 'chandra channel test discord' to retry.")
			}

			// Confirm before adding the replying user to the allowlist (design §2).
			var confirmed bool
			confirmForm := huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Add %s as an authorized user?", result.ReplyUsername)).
					Value(&confirmed),
			))
			if confirmErr := confirmForm.Run(); confirmErr != nil || !confirmed {
				fmt.Println("Skipped. You can add users later with: chandra access add")
			} else {
				if err := addUserToAllowlist(opts.DBPath, discordChannelID, result.ReplyUserID, result.ReplyUsername); err != nil {
					fmt.Printf("  Warning: could not add %s to allowlist: %v\n", result.ReplyUsername, err)
				} else {
					fmt.Printf("%s bootstrapped as authorized user\n", result.ReplyUsername)
				}
			}
		}
	}

	// --- Stage 5b: Daemon install stub ---
	// Full daemon install (systemd/launchd) is deferred (design §10 "ship second", requires daemon lifecycle work).
	fmt.Println()
	fmt.Println("To run Chandra persistently, start the daemon manually:")
	fmt.Println("  chandrad               — background daemon")
	fmt.Println("  chandrad --foreground  — foreground (useful for debugging)")
	fmt.Println("  (Automated service install coming in a future release)")

	// --- Stage 6: Final doctor pass ---
	// Uses the exact same DoctorCheck implementations as `chandra doctor` — the two
	// commands are guaranteed to agree on what "healthy" means (design §4).
	fmt.Println("\nRunning final verification...")
	cfg, _ := config.Load(opts.ConfigPath)
	finalChecks := []doctor.Check{
		doctor.NewConfigCheck(opts.ConfigPath),
		doctor.NewPermissionsCheck(cfgDir, opts.ConfigPath),
		doctor.NewDBCheck(opts.DBPath),
	}
	if cfg != nil {
		finalChecks = append(finalChecks, doctor.NewProviderCheck(&cfg.Provider))
		// (open-policy warning is injected into results after RunAll — see below)
		// AllowlistCheck reads the DB rows bootstrapped by Hello World. Only run
		// when loopSucceeded: if Hello World timed out, the allowlist is
		// intentionally empty and the user retries with 'chandra channel test
		// discord'. Running it unconditionally would hard-fail and leave the
		// checkpoint in place, contradicting the "channel failures are always
		// skippable" requirement (design §1.1). The role-policy config validation
		// (access_policy=role with no roles) is caught earlier by the Pre-Stage-2
		// guard, so AllowlistCheck does not need to run for that purpose.
		if loopSucceeded && cfg.Channels.Discord != nil {
			finalChecks = append(finalChecks, doctor.NewAllowlistCheck(cfg, opts.DBPath))
		}
		if loopSucceeded && cfg.Channels.Discord != nil && cfg.Channels.Discord.BotToken != "" {
			// ChannelVerifiedCheck reads DB rows written by the Hello World bootstrap.
			// Guarded by loopSucceeded: if Hello World timed out, the DB is
			// intentionally empty and the user retries with 'chandra channel test
			// discord' — this is skippable per design §1.1.
			for _, chID := range cfg.Channels.Discord.ChannelIDs {
				finalChecks = append(finalChecks, doctor.NewChannelVerifiedCheck(chID, opts.DBPath))
			}
		}
		finalChecks = append(finalChecks, doctor.NewSchedulerCheck(""))
		finalChecks = append(finalChecks, doctor.NewDaemonCheck(""))
	}

	results := doctor.RunAll(ctx, finalChecks, 10)
	// Inject open-policy warning directly into results so doctor.AnyWarned()
	// picks it up and the final banner ("Setup complete with warnings") is
	// consistent — the warning is never followed by "Everything looks good".
	// This is independent of loopSucceeded: access_policy=open is a config
	// concern, not a live-connectivity concern.
	if cfg != nil && cfg.Channels.Discord != nil && cfg.Channels.Discord.AccessPolicy == "open" {
		results = append(results, doctor.CheckResult{
			Name:   "AccessPolicy",
			Status: doctor.Warn,
			Detail: "access_policy=open — any Discord user can message the bot",
			Fix:    "set access_policy = \"invite\" in config, then run: chandra channel test discord",
		})
	}
	for _, r := range results {
		switch r.Status {
		case doctor.Pass:
			fmt.Printf("  %-14s done %s\n", r.Name, r.Detail)
		case doctor.Warn:
			fmt.Printf("  %-14s warn %s\n", r.Name, r.Detail)
		case doctor.Fail:
			fmt.Printf("  %-14s fail %s\n", r.Name, r.Detail)
		}
	}

	if doctor.AnyFailed(results) {
		fmt.Println("\nSetup encountered issues. Run 'chandra doctor' for details.")
		return nil // keep checkpoint so init can be resumed after fixing
	}

	_ = DeleteCheckpoint(cpPath)

	if doctor.AnyWarned(results) {
		fmt.Println("\nSetup complete with warnings (see above).")
		fmt.Println("Start the daemon with 'chandrad' to clear remaining items.")
		fmt.Println("Re-run 'chandra doctor' after starting the daemon.")
		return nil
	}

	fmt.Println("\nEverything looks good. Chandra is ready.")
	fmt.Println("Start the daemon with 'chandrad', then use 'chandra' commands to interact.")
	return nil
}

func runProviderStage(ctx context.Context, providerType, apiKey, model *string) error {
	// Step 1: select provider type
	form1 := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Which provider will you use?").
			Options(
				huh.NewOption("OpenAI (api.openai.com)", "openai"),
				huh.NewOption("Anthropic (api.anthropic.com)", "anthropic"),
				huh.NewOption("OpenRouter (openrouter.ai) — access to 100+ models", "openrouter"),
				huh.NewOption("Ollama (local)", "ollama"),
				huh.NewOption("Custom OpenAI-compatible endpoint (HTTPS required)", "custom"),
			).
			Value(providerType),
	))
	if err := form1.Run(); err != nil {
		return err
	}

	// Step 2: for custom provider, prompt for the base URL
	if *providerType == "custom" {
		var customURL string
		form2 := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Custom provider base URL (HTTPS required)").
				Placeholder("https://my-llm.example.com/v1").
				Value(&customURL),
		))
		if err := form2.Run(); err != nil {
			return err
		}
		// Validate HTTPS requirement before accepting
		if len(customURL) < 8 || customURL[:8] != "https://" {
			return fmt.Errorf("custom provider URL must use HTTPS (got: %s)", customURL)
		}
		// Stash custom URL in providerType encoded form — writeConfig will extract it
		// by checking if providerType == "custom" and reading from the customBaseURL.
		// Simplest: use a package-level var (acceptable for a wizard flow).
		customBaseURL = customURL
	}

	// Step 3: prompt for API key and model
	defaultModel := defaultModelForProvider(*providerType)

	// For OpenRouter, show a curated model picker instead of a free-text field.
	// OpenRouter exposes 100+ models using provider/model-name format; a plain
	// text field with a placeholder doesn't help users who don't know the IDs.
	if *providerType == "openrouter" {
		const orCustomSentinel = "__custom__"
		selectedModel := defaultModel
		form3a := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Enter your OpenRouter API key").
				EchoMode(huh.EchoModePassword).
				Value(apiKey),
			huh.NewSelect[string]().
				Title("Model").
				Description("Popular models — pricing at openrouter.ai/models").
				Options(
					huh.NewOption("Claude Sonnet 4.5  (anthropic/claude-sonnet-4-5)", "anthropic/claude-sonnet-4-5"),
					huh.NewOption("Claude Haiku 3.5   (anthropic/claude-3-5-haiku)", "anthropic/claude-3-5-haiku"),
					huh.NewOption("Claude Opus 4.5    (anthropic/claude-opus-4-5)", "anthropic/claude-opus-4-5"),
					huh.NewOption("GPT-4o             (openai/gpt-4o)", "openai/gpt-4o"),
					huh.NewOption("GPT-4o mini        (openai/gpt-4o-mini)", "openai/gpt-4o-mini"),
					huh.NewOption("Gemini 2.0 Flash   (google/gemini-2.0-flash-001)", "google/gemini-2.0-flash-001"),
					huh.NewOption("Llama 3.3 70B      (meta-llama/llama-3.3-70b-instruct)", "meta-llama/llama-3.3-70b-instruct"),
					huh.NewOption("Custom — enter model ID", orCustomSentinel),
				).
				Value(&selectedModel),
		))
		if err := form3a.Run(); err != nil {
			return err
		}
		if selectedModel == orCustomSentinel {
			var customModel string
			form3b := huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Model ID").
					Description("Format: provider/model-name  e.g. mistralai/mistral-7b-instruct").
					Placeholder("provider/model-name").
					Value(&customModel),
			))
			if err := form3b.Run(); err != nil {
				return err
			}
			if customModel == "" {
				customModel = defaultModel
			}
			*model = customModel
		} else {
			*model = selectedModel
		}
	} else {
		form3 := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Enter your API key (leave blank for Ollama)").
				EchoMode(huh.EchoModePassword).
				Value(apiKey),
			huh.NewInput().
				Title("Default model").
				Placeholder(defaultModel).
				Value(model),
		))
		if err := form3.Run(); err != nil {
			return err
		}
		if *model == "" {
			*model = defaultModel
		}
	}

	// Test the provider connection before proceeding (design §1 init stage table:
	// "Provider (required)"). Build a temporary config for the check.
	baseURL := providerBaseURL(*providerType)
	if *providerType == "custom" && customBaseURL != "" {
		baseURL = customBaseURL
	}
	testCfg := config.ProviderConfig{
		Type:         *providerType,
		BaseURL:      baseURL,
		APIKey:       *apiKey,
		DefaultModel: *model,
	}
	check := doctor.NewProviderCheck(&testCfg)
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result := check.Run(checkCtx)

	switch result.Status {
	case doctor.Pass:
		fmt.Printf("Provider connection verified (%s)\n", *model)
	case doctor.Warn:
		fmt.Printf("Provider check warning: %s\n", result.Detail)
		// Continue — a warning is non-fatal
	case doctor.Fail:
		fmt.Printf("Provider check failed: %s\n", result.Detail)
		if result.Fix != "" {
			fmt.Printf("  Fix: %s\n", result.Fix)
		}
		var skip bool
		skipForm := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().
				Title("Provider check failed. Continue anyway?").
				Description("Verify connectivity later with 'chandra provider test'.").
				Value(&skip),
		))
		if err := skipForm.Run(); err != nil {
			return err
		}
		if !skip {
			return fmt.Errorf("provider check failed: %s", result.Detail)
		}
	}
	return nil
}

// customBaseURL holds the URL entered for a custom provider during init.
// Package-level to avoid threading it through all function signatures.
var customBaseURL string

func defaultModelForProvider(t string) string {
	switch t {
	case "openai":
		return "gpt-4o"
	case "anthropic":
		return "claude-sonnet-4-6"
	case "openrouter":
		return "openai/gpt-4o"
	case "ollama":
		return "llama3"
	default:
		return ""
	}
}

func runChannelStage(ctx context.Context, discordToken, channelID, accessPolicy *string, discordRoleIDs *[]string) error {
	var useDiscord bool
	form1 := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Set up Discord?").
			Description("CLI always works without Discord.").
			Value(&useDiscord),
	))
	if err := form1.Run(); err != nil {
		return err
	}
	if !useDiscord {
		return nil
	}

	form2 := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Discord bot token").
			EchoMode(huh.EchoModePassword).
			Value(discordToken),
		huh.NewInput().
			Title("Channel ID to post the setup verification message").
			Value(channelID),
		huh.NewSelect[string]().
			Title("Who can talk to Chandra on Discord?").
			Options(
				huh.NewOption("Invite codes — you share codes, users redeem them", "invite"),
				huh.NewOption("Request flow — anyone can request, you approve", "request"),
				huh.NewOption("Role-based — trust members of a Discord role", "role"),
				huh.NewOption("Allowlist — paste user IDs manually", "allowlist"),
			).
			Value(accessPolicy),
	))
	if err := form2.Run(); err != nil {
		return err
	}
	// Validate required fields — a token without a channel ID produces a broken config.
	if *discordToken == "" {
		return fmt.Errorf("Discord bot token must not be empty")
	}
	if *channelID == "" {
		return fmt.Errorf("Discord channel ID must not be empty")
	}

	// Role-based access requires at least one role ID to be non-empty on day one.
	if *accessPolicy == "role" {
		var roleIDsStr string
		roleForm := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Discord role ID(s) to trust (comma-separated)").
				Description("Server Settings -> Roles -> right-click a role -> Copy Role ID").
				Placeholder("1234567890,9876543210").
				Value(&roleIDsStr),
		))
		if err := roleForm.Run(); err != nil {
			return err
		}
		for _, id := range strings.Split(roleIDsStr, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				*discordRoleIDs = append(*discordRoleIDs, id)
			}
		}
		if len(*discordRoleIDs) == 0 {
			return fmt.Errorf("role-based access requires at least one role ID")
		}
	}
	return nil
}

func writeConfig(cfgPath, providerType, apiKey, model, agentName, description, discordToken, discordChannelID, accessPolicy string, discordRoleIDs []string, dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		return err
	}

	baseURL := providerBaseURL(providerType)
	if providerType == "custom" && customBaseURL != "" {
		baseURL = customBaseURL
	}
	// embedding_model is now a field inside [provider], not a separate section.
	// Anthropic has no native embedding API — fall back to OpenAI's model name.
	embeddingModel := providerEmbeddingModel(providerType)

	discordSection := ""
	if discordToken != "" {
		// Build allowed_roles line — only populated for "role" access policy.
		allowedRolesLine := "allowed_roles = []"
		if accessPolicy == "role" && len(discordRoleIDs) > 0 {
			quoted := make([]string, len(discordRoleIDs))
			for i, id := range discordRoleIDs {
				quoted[i] = fmt.Sprintf("%q", id)
			}
			allowedRolesLine = fmt.Sprintf("allowed_roles = [%s]", strings.Join(quoted, ", "))
		}
		discordSection = fmt.Sprintf(`
[channels.discord]
enabled = true
bot_token = %q
channel_ids = [%q]
access_policy = %q
allowed_users = []
%s
`, discordToken, discordChannelID, accessPolicy, allowedRolesLine)
	}

	// Uses design schema: [identity], provider.default_model, provider.embedding_model
	// No separate [embeddings] section.
	content := fmt.Sprintf(`# Chandra Configuration
# Generated by 'chandra init' — edit freely

[identity]
name = %q
description = %q

[database]
path = %q

[provider]
type = %q
base_url = %q
api_key = %q
default_model = %q
embedding_model = %q
%s`, agentName, description, dbPath, providerType, baseURL, apiKey, model, embeddingModel, discordSection)

	return os.WriteFile(cfgPath, []byte(content), 0600)
}

func providerBaseURL(t string) string {
	switch t {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	default:
		return ""
	}
}

func providerEmbeddingModel(t string) string {
	switch t {
	case "openai", "openrouter":
		return "text-embedding-3-small"
	case "anthropic":
		// Anthropic has no native embedding API.
		// Return the OpenAI model name — the implementer must configure a
		// separate embeddings provider or use OpenAI-compatible embeddings.
		return "text-embedding-3-small"
	case "ollama":
		return "mxbai-embed-large"
	default:
		return "text-embedding-3-small"
	}
}

// ---------------------------------------------------------------------------
// runSemanticStage prompts the user about long-term semantic memory via Ollama.
// Returns the embeddings base URL, model name, and dimensions to write to config.
// Returns empty strings when the user declines or the install fails.
// ---------------------------------------------------------------------------

const (
	ollamaEmbedModel      = "nomic-embed-text"
	ollamaEmbedDimensions = 768
	ollamaBaseURL         = "http://localhost:11434/v1"
)

func runSemanticStage(ctx context.Context) (baseURL, model string, dimensions int, err error) {
	fmt.Println()

	var enable bool
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Enable long-term semantic memory?").
			Description("Allows Chandra to recall facts from past conversations by relevance, not just recency.\nUses Ollama (local AI, no cloud API) with the nomic-embed-text model (~274 MB download).\nYou can skip this and add it later with: chandra config set-embeddings").
			Value(&enable),
	))
	if runErr := form.Run(); runErr != nil {
		return "", "", 0, nil // treat form error as user cancel
	}
	if !enable {
		fmt.Println("Semantic memory skipped. Enable later with: chandra config set-embeddings")
		return "", "", 0, nil
	}

	// Check if Ollama is already installed.
	ollamaInstalled := false
	if path, lookErr := exec.LookPath("ollama"); lookErr == nil {
		_ = path
		ollamaInstalled = true
	}

	if !ollamaInstalled {
		fmt.Println("\nInstalling Ollama...")
		fmt.Println("  This runs the official Ollama install script (https://ollama.com/install.sh)")
		fmt.Println("  Sudo access is required.")

		installCmd := exec.CommandContext(ctx, "bash", "-c", "curl -fsSL https://ollama.com/install.sh | sh")
		installCmd.Stdout = os.Stdout
		installCmd.Stderr = os.Stderr
		if installErr := installCmd.Run(); installErr != nil {
			return "", "", 0, fmt.Errorf("Ollama install failed: %w", installErr)
		}
		fmt.Println("Ollama installed.")
	} else {
		fmt.Println("Ollama already installed.")
	}

	// Ensure Ollama service is running.
	startCmd := exec.CommandContext(ctx, "bash", "-c", "systemctl is-active ollama >/dev/null 2>&1 || (ollama serve >/dev/null 2>&1 &)")
	_ = startCmd.Run()

	// Check if the model is already pulled.
	modelPresent := false
	listOut, listErr := exec.CommandContext(ctx, "ollama", "list").Output()
	if listErr == nil && strings.Contains(string(listOut), ollamaEmbedModel) {
		modelPresent = true
	}

	if !modelPresent {
		fmt.Printf("\nDownloading %s model (~274 MB)...\n", ollamaEmbedModel)
		pullCmd := exec.CommandContext(ctx, "ollama", "pull", ollamaEmbedModel)
		pullCmd.Stdout = os.Stdout
		pullCmd.Stderr = os.Stderr
		if pullErr := pullCmd.Run(); pullErr != nil {
			return "", "", 0, fmt.Errorf("model pull failed: %w", pullErr)
		}
	} else {
		fmt.Printf("%s already downloaded.\n", ollamaEmbedModel)
	}

	// Quick smoke test: request one embedding to confirm the endpoint is live.
	testCmd := exec.CommandContext(ctx, "bash", "-c",
		`curl -s -X POST http://localhost:11434/v1/embeddings `+
			`-H "Content-Type: application/json" `+
			`-d '{"model":"nomic-embed-text","input":["test"]}' `+
			`| grep -q '"embedding"'`,
	)
	if testErr := testCmd.Run(); testErr != nil {
		return "", "", 0, fmt.Errorf("Ollama endpoint test failed — is the service running? (%w)", testErr)
	}

	fmt.Println("Semantic memory enabled via Ollama (nomic-embed-text).")
	return ollamaBaseURL, ollamaEmbedModel, ollamaEmbedDimensions, nil
}


// persistChannelVerified records a successful Hello World loop in channel_verifications.
// Always called on loop success — ChannelVerifiedCheck reads this table.
func persistChannelVerified(dbPath, channelID, userID string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(
		`INSERT OR REPLACE INTO channel_verifications (channel_id, verified_at, verified_user_id) VALUES (?, ?, ?)`,
		channelID, time.Now().Unix(), userID,
	)
	return err
}

// addUserToAllowlist writes a row to allowed_users. Called unconditionally on
// loop success — the Hello World reply bootstraps the allowlist automatically
// (design §346: "The Hello World reply bootstraps the allowlist — no manual ID lookup needed").
func addUserToAllowlist(dbPath, channelID, userID, username string) error {
	st, err := store.NewDB(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return err
	}
	_, err = st.DB().Exec(
		`INSERT OR IGNORE INTO allowed_users (channel_id, user_id, username, source, added_at) VALUES (?, ?, ?, 'hello_world', ?)`,
		channelID, userID, username, time.Now().Unix(),
	)
	return err
}

func checkpointSummary(cp *Checkpoint) string {
	var parts []string
	mark := func(done bool, name string) string {
		if done {
			return name + " done"
		}
		return name + " pending"
	}
	parts = append(parts, mark(cp.ProviderDone, "provider"))
	parts = append(parts, mark(cp.ChannelsDone, "channels"))
	parts = append(parts, mark(cp.IdentityDone, "identity"))
	parts = append(parts, mark(cp.ConfigWritten, "config"))
	return "Progress: " + strings.Join(parts, "  ")
}
