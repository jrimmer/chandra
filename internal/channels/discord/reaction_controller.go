package discord

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// StatusEmojis defines the emoji set used for status reactions.
type StatusEmojis struct {
	Queued    string // 👀 message received/queued
	Thinking  string // 🤔 LLM is generating
	Tool      string // 🔥 generic tool running
	Coding    string // 👨‍💻 exec/read/write/edit tool running
	Web       string // ⚡ web/search/fetch tool running
	Done      string // 👍 completed successfully
	Error     string // 😱 error occurred
	StallSoft string // 🥱 slow (10s without progress)
	StallHard string // 😨 likely hung (30s without progress)
}

// StatusTiming controls debounce and hold durations.
type StatusTiming struct {
	DebounceMs  int // transition debounce (default 700ms); Queued is always immediate
	StallSoftMs int // stall soft threshold (default 10s)
	StallHardMs int // stall hard threshold (default 30s)
	DoneHoldMs  int // hold Done reaction before removing (default 1500ms)
	ErrorHoldMs int // hold Error reaction before removing (default 2500ms)
}

var defaultEmojis = StatusEmojis{
	Queued:    "👀",
	Thinking:  "🤔",
	Tool:      "🔥",
	Coding:    "👨‍💻",
	Web:       "⚡",
	Done:      "👍",
	Error:     "😱",
	StallSoft: "🥱",
	StallHard: "😨",
}

var defaultTiming = StatusTiming{
	DebounceMs:  700,
	StallSoftMs: 10000,
	StallHardMs: 30000,
	DoneHoldMs:  1500,
	ErrorHoldMs: 2500,
}

// webToolTokens and codeToolTokens drive resolveToolEmoji.
var webToolTokens = []string{"web_search", "search", "fetch", "browse", "scrape", "http"}
var codeToolTokens = []string{"exec", "read_file", "write_file", "read", "write", "edit", "process", "bash", "shell"}

// resolveToolEmoji maps a tool name to its status emoji.
func resolveToolEmoji(toolName string, emojis StatusEmojis) string {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	for _, tok := range webToolTokens {
		if strings.Contains(lower, tok) {
			return emojis.Web
		}
	}
	for _, tok := range codeToolTokens {
		if strings.Contains(lower, tok) {
			return emojis.Coding
		}
	}
	return emojis.Tool
}

// StatusReactionController manages emoji reactions on a user's inbound message
// to provide real-time processing feedback. Transitions are debounced and
// serialised to prevent concurrent Discord API calls and emoji flickering.
//
// Lifecycle:
//  1. Create with NewStatusReactionController (starts stall timers).
//  2. Call SetQueued immediately (👀 — immediate, no debounce).
//  3. Call SetThinking when LLM call starts.
//  4. Call SetTool(name) before each tool execution.
//  5. Call SetDone or SetError when the turn completes.
//  6. The controller self-destructs after Done/Error hold period.
type StatusReactionController struct {
	session   *discordgo.Session
	channelID string
	messageID string
	emojis    StatusEmojis
	timing    StatusTiming

	mu           sync.Mutex
	current      string       // currently active reaction emoji
	finished     bool         // true after Done/Error; blocks further transitions
	debounce     *time.Timer  // pending state change timer
	stallSoft    *time.Timer
	stallHard    *time.Timer

	// ops serialises all Discord API calls (add + remove) so they never race.
	ops chan func()
	wg  sync.WaitGroup
}

// NewStatusReactionController creates a controller for the given message.
// The caller must call SetQueued immediately after creation.
func NewStatusReactionController(session *discordgo.Session, channelID, messageID string, emojis *StatusEmojis, timing *StatusTiming) *StatusReactionController {
	e := defaultEmojis
	if emojis != nil {
		e = *emojis
	}
	t := defaultTiming
	if timing != nil {
		t = *timing
	}
	c := &StatusReactionController{
		session:   session,
		channelID: channelID,
		messageID: messageID,
		emojis:    e,
		timing:    t,
		ops:       make(chan func(), 16),
	}

	// Start the serialising worker goroutine.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for fn := range c.ops {
			fn()
		}
	}()

	// Initialise stall timers.
	c.resetStallTimers()
	return c
}

// SetQueued sets the 👀 reaction immediately (no debounce).
func (c *StatusReactionController) SetQueued() {
	c.scheduleEmoji(c.emojis.Queued, true)
}

// SetThinking transitions to the 🤔 (thinking) state.
func (c *StatusReactionController) SetThinking() {
	c.scheduleEmoji(c.emojis.Thinking, false)
}

// SetTool transitions to the appropriate tool emoji based on the tool name.
func (c *StatusReactionController) SetTool(toolName string) {
	c.scheduleEmoji(resolveToolEmoji(toolName, c.emojis), false)
}

// SetDone transitions to ✅ (done), holds for DoneHoldMs, then removes the reaction.
func (c *StatusReactionController) SetDone() {
	c.finishWithEmoji(c.emojis.Done, time.Duration(c.timing.DoneHoldMs)*time.Millisecond)
}

// SetError transitions to 😱 (error), holds for ErrorHoldMs, then removes the reaction.
func (c *StatusReactionController) SetError() {
	c.finishWithEmoji(c.emojis.Error, time.Duration(c.timing.ErrorHoldMs)*time.Millisecond)
}

// Finish tears down the controller cleanly — removes the current reaction
// without adding a Done/Error emoji. Called when edit-in-place handles the
// final delivery state and no terminal reaction is desired.
func (c *StatusReactionController) Finish() {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	if c.debounce != nil {
		c.debounce.Stop()
		c.debounce = nil
	}
	c.clearStallTimersLocked()
	prev := c.current
	c.current = ""
	c.mu.Unlock()

	// Remove current reaction (if any), then close.
	if prev != "" {
		c.ops <- func() {
			_ = c.session.MessageReactionRemove(c.channelID, c.messageID, prev, "@me")
		}
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(c.ops)
	}()
}

// Finish tears down the controller cleanly — removes the current reaction
// without adding a Done/Error emoji. Called when edit-in-place handles the
// final delivery state and no terminal reaction is desired.

// Finish tears down the controller cleanly — removes the current reaction
// without adding a Done/Error emoji. Called when edit-in-place handles the
// final delivery state and no terminal reaction is desired.

// scheduleEmoji queues an emoji transition. If immediate is false, the
// transition is debounced by timing.DebounceMs.
func (c *StatusReactionController) scheduleEmoji(emoji string, immediate bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.finished {
		return
	}
	if emoji == c.current {
		return
	}

	// Cancel any pending debounced transition.
	if c.debounce != nil {
		c.debounce.Stop()
		c.debounce = nil
	}

	// Reset stall timers on every state change attempt.
	c.resetStallTimersLocked()

	if immediate {
		prev := c.current
		c.current = emoji
		c.enqueueTransition(prev, emoji)
	} else {
		target := emoji
		c.debounce = time.AfterFunc(time.Duration(c.timing.DebounceMs)*time.Millisecond, func() {
			c.mu.Lock()
			if c.finished || c.current == target {
				c.mu.Unlock()
				return
			}
			prev := c.current
			c.current = target
			c.debounce = nil
			c.mu.Unlock()
			c.enqueueTransition(prev, target)
		})
	}
}

// finishWithEmoji sets a terminal emoji, stops stall timers, holds, then
// removes the reaction and closes the ops channel.
func (c *StatusReactionController) finishWithEmoji(emoji string, hold time.Duration) {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	if c.debounce != nil {
		c.debounce.Stop()
		c.debounce = nil
	}
	c.clearStallTimersLocked()
	prev := c.current
	c.current = emoji
	c.mu.Unlock()

	// Enqueue: set final emoji, hold, remove, then close ops channel.
	c.enqueueTransition(prev, emoji)
	c.ops <- func() {
		time.Sleep(hold)
		_ = c.session.MessageReactionRemove(c.channelID, c.messageID, emoji, "@me")
	}
	// Close after all queued ops drain.
	go func() {
		// Enqueue the close signal via a nil fn sentinel would be fragile;
		// instead just close after a generous drain window.
		// The worker goroutine will process the above ops first (FIFO channel).
		time.Sleep(hold + 200*time.Millisecond)
		close(c.ops)
	}()
}

// enqueueTransition pushes an add-new / remove-old pair onto the ops channel.
// Must NOT be called while holding c.mu.
func (c *StatusReactionController) enqueueTransition(prev, next string) {
	c.ops <- func() {
		// Add the new reaction first so there's always a status visible.
		if err := c.session.MessageReactionAdd(c.channelID, c.messageID, next); err != nil {
			slog.Warn("discord: status reaction add failed",
				"emoji", next, "message", c.messageID, "err", err)
		}
		// Remove the previous reaction (if any) after adding the new one.
		if prev != "" {
			if err := c.session.MessageReactionRemove(c.channelID, c.messageID, prev, "@me"); err != nil {
				slog.Debug("discord: status reaction remove failed",
					"emoji", prev, "message", c.messageID, "err", err)
			}
		}
	}
}

// resetStallTimers resets both stall timers. Must be called without c.mu held.
func (c *StatusReactionController) resetStallTimers() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetStallTimersLocked()
}

// resetStallTimersLocked resets stall timers. Caller must hold c.mu.
func (c *StatusReactionController) resetStallTimersLocked() {
	c.clearStallTimersLocked()

	softMs := c.timing.StallSoftMs
	hardMs := c.timing.StallHardMs

	c.stallSoft = time.AfterFunc(time.Duration(softMs)*time.Millisecond, func() {
		c.scheduleEmoji(c.emojis.StallSoft, true)
	})
	c.stallHard = time.AfterFunc(time.Duration(hardMs)*time.Millisecond, func() {
		c.scheduleEmoji(c.emojis.StallHard, true)
	})
}

// clearStallTimersLocked stops and nils the stall timers. Caller must hold c.mu.
func (c *StatusReactionController) clearStallTimersLocked() {
	if c.stallSoft != nil {
		c.stallSoft.Stop()
		c.stallSoft = nil
	}
	if c.stallHard != nil {
		c.stallHard.Stop()
		c.stallHard = nil
	}
}

// Wait blocks until the ops worker goroutine exits (after finishWithEmoji closes ops).
// Useful in tests. In production the controller is fire-and-forget.
func (c *StatusReactionController) Wait() {
	c.wg.Wait()
}
