// Package scheduler provides a tick-based engine that reads due intents and
// emits ScheduledTurns for the agent loop to process.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
)

// ScheduledTurn represents a proactive agent turn triggered by a due intent.
type ScheduledTurn struct {
	IntentID  string
	Prompt    string
	SessionID string
}

// Scheduler defines the contract for the tick-based intent evaluation engine.
type Scheduler interface {
	Start(ctx context.Context) error
	Stop() error
	Turns() <-chan ScheduledTurn
}

// Compile-time assertion that *scheduler satisfies Scheduler.
var _ Scheduler = (*scheduler)(nil)

// scheduler is the internal implementation of Scheduler.
type scheduler struct {
	intentStore  intent.IntentStore
	tickInterval time.Duration
	turns        chan ScheduledTurn

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler constructs a new scheduler. The turnsBufferSize controls the
// capacity of the ScheduledTurn channel; a value of 0 uses a default of 10.
func NewScheduler(intentStore intent.IntentStore, tickInterval time.Duration, turnsBufferSize int) *scheduler {
	if turnsBufferSize <= 0 {
		turnsBufferSize = 10
	}
	return &scheduler{
		intentStore:  intentStore,
		tickInterval: tickInterval,
		turns:        make(chan ScheduledTurn, turnsBufferSize),
	}
}

// Start launches the background tick goroutine. The provided context controls
// external cancellation; Stop() provides cooperative shutdown.
// Returns an error if the scheduler is already running.
func (s *scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return fmt.Errorf("scheduler: already started")
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(ctx)
	}()
	return nil
}

// Stop cancels the internal context and waits for the goroutine to exit.
func (s *scheduler) Stop() error {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	return nil
}

// Turns returns the channel on which ScheduledTurns are sent.
func (s *scheduler) Turns() <-chan ScheduledTurn {
	return s.turns
}

// run is the main loop executed by the background goroutine.
func (s *scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick evaluates all due intents and emits ScheduledTurns.
func (s *scheduler) tick(ctx context.Context) {
	due, err := s.intentStore.Due(ctx)
	if err != nil {
		slog.Error("scheduler: fetch due intents", "err", err)
		return
	}

	for _, in := range due {
		turn := ScheduledTurn{
			IntentID:  in.ID,
			Prompt:    in.Action,
			SessionID: "scheduler",
		}

		// Non-blocking send: drop and warn if the channel is full.
		select {
		case s.turns <- turn:
		default:
			slog.Warn("scheduler: turns channel full, dropping intent", "id", in.ID)
		}

		// Advance scheduling times regardless of whether the turn was delivered.
		in.LastChecked = time.Now()
		in.NextCheck = time.Now().Add(s.tickInterval)
		if err := s.intentStore.Update(ctx, in); err != nil {
			slog.Error("scheduler: update intent after tick", "id", in.ID, "err", err)
		}
	}
}
