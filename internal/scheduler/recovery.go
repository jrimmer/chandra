package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/jrimmer/chandra/internal/memory/intent"
)

// RecoverMissedJobs inspects all active intents at daemon startup and reschedules
// overdue recurring intents so they fire in a staggered sequence rather than all
// at once on the first scheduler tick.
//
// The problem it solves: if the daemon was offline for hours, all recurring intents
// (heartbeat, health checks, cron-based skills) will have a next_check in the past.
// Without recovery, all of them fire simultaneously on the first tick — a burst of
// LLM calls that may exhaust concurrency limits and flood the channel.
//
// Recovery rules:
//   - One-shot intents (RecurrenceInterval == 0): left alone. They fire on the
//     first tick, which is correct — the user set a reminder and should receive it,
//     even if slightly late.
//   - Recurring intents that are overdue: rescheduled to fire at now + stagger,
//     where stagger increments by staggerStep for each recovered intent. This
//     spreads the startup burst over a configurable window.
//
// Returns the count of intents that were rescheduled.
func RecoverMissedJobs(ctx context.Context, intentStore intent.IntentStore, staggerStep time.Duration) (int, error) {
	if staggerStep <= 0 {
		staggerStep = 5 * time.Second
	}

	active, err := intentStore.Active(ctx)
	if err != nil {
		return 0, err
	}

	now := time.Now()
	var recovered int
	var stagger time.Duration

	for _, in := range active {
		// Skip one-shot intents: they should fire as soon as possible.
		if in.RecurrenceInterval == 0 {
			continue
		}
		// Skip intents whose next_check is in the future.
		if in.NextCheck.After(now) {
			continue
		}

		// Recurring intent is overdue. Reschedule to now + stagger to avoid burst.
		stagger += staggerStep
		nextCheck := now.Add(stagger)
		missedBy := now.Sub(in.NextCheck).Round(time.Second)

		if err := intentStore.Reschedule(ctx, in.ID, nextCheck); err != nil {
			slog.Warn("scheduler: missed-job recovery: reschedule failed",
				"id", in.ID, "err", err)
			continue
		}

		slog.Info("scheduler: missed-job recovery: rescheduled overdue recurring intent",
			"id", in.ID,
			"description", in.Description,
			"missed_by", missedBy.String(),
			"new_next_check", nextCheck.Format(time.RFC3339),
			"recurrence", in.RecurrenceInterval.String(),
		)
		recovered++
	}

	if recovered > 0 {
		slog.Info("scheduler: missed-job recovery complete",
			"recovered", recovered,
			"stagger_step", staggerStep.String(),
		)
	}

	return recovered, nil
}
