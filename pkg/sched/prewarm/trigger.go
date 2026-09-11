// Package prewarm drives durable, time-windowed capacity restoration.
// It deliberately contains no HTTP or SQL: API/cron/pattern producers create
// state.PrewarmIntent rows, while this package owns the single scheduler
// transition from pending intent to an engine admission.
package prewarm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	DefaultLeadTime  = 60 * time.Second
	DefaultInterval  = time.Second
	DefaultBatchSize = 32

	TriggerName = "prewarm"
)

// Engine is the narrow scheduler admission seam. The implementation must
// preserve the normal ledger, placement, wake-rate and plan gates. The count
// is a desired capacity hint; it is never permission to exceed those gates.
type Engine interface {
	Prewarm(ctx context.Context, appID string, count int) (admitted int, err error)
}

// IntentStore is the durable queue used by Trigger. state.PrewarmStore is the
// production implementation; the local interface keeps this package easy to
// exercise with a small fake.
type IntentStore interface {
	ListDuePrewarmIntents(ctx context.Context, before, now time.Time, limit int) ([]state.PrewarmIntent, error)
	ClaimPrewarmIntent(ctx context.Context, id string, claimedAt time.Time) (state.PrewarmIntent, bool, error)
	CompletePrewarmIntent(ctx context.Context, id string, firedAt time.Time, admittedCount int, outcome string) error
	FailPrewarmIntent(ctx context.Context, id string, firedAt time.Time, cause string) error
}

// Auditor is the narrow audit seam used for scheduler-side prewarm events.
// Keeping it local avoids coupling this package to the concrete audit
// implementation while allowing schedd to attribute events to actor=schedd.
type Auditor interface {
	Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
}

type Options struct {
	LeadTime  time.Duration
	Interval  time.Duration
	BatchSize int
	Clock     func() time.Time
	Logger    *slog.Logger
	Auditor   Auditor
}

type Trigger struct {
	store     IntentStore
	engine    Engine
	leadTime  time.Duration
	interval  time.Duration
	batchSize int
	now       func() time.Time
	log       *slog.Logger
	auditor   Auditor
}

func New(store IntentStore, engine Engine, opts Options) *Trigger {
	if opts.LeadTime <= 0 {
		opts.LeadTime = DefaultLeadTime
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultBatchSize
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Trigger{store: store, engine: engine, leadTime: opts.LeadTime, interval: opts.Interval, batchSize: opts.BatchSize, now: opts.Clock, log: opts.Logger, auditor: opts.Auditor}
}

func (t *Trigger) Interval() time.Duration {
	if t == nil {
		return 0
	}
	return t.interval
}

// Tick claims each intent whose window is within LeadTime, then asks the
// engine to restore the requested capacity. Claiming is atomic in the store,
// so multiple schedd processes cannot duplicate a prewarm.
func (t *Trigger) Tick(ctx context.Context) error {
	if t == nil || t.store == nil || t.engine == nil {
		return nil
	}
	now := t.now().UTC()
	intents, err := t.store.ListDuePrewarmIntents(ctx, now.Add(t.leadTime), now, t.batchSize)
	if err != nil {
		return fmt.Errorf("prewarm: list due intents: %w", err)
	}
	for _, candidate := range intents {
		intent, claimed, claimErr := t.store.ClaimPrewarmIntent(ctx, candidate.ID, now)
		if claimErr != nil {
			if errors.Is(claimErr, context.Canceled) {
				return claimErr
			}
			t.log.Warn("prewarm: claim failed", "intent_id", candidate.ID, "err", claimErr)
			continue
		}
		if !claimed {
			continue
		}
		admitted, admitErr := t.engine.Prewarm(ctx, intent.AppID, intent.Count)
		if admitErr != nil {
			if errors.Is(admitErr, context.Canceled) {
				return admitErr
			}
			// Preserve a partial success as a completed intent so the
			// temporary floor reflects the instances that really made it
			// live. A later intent can add the remaining capacity without
			// making this row replay already-admitted wakes.
			if admitted > 0 {
				partial := fmt.Sprintf("partial:%d:%s", admitted, admitErr.Error())
				if markErr := t.store.CompletePrewarmIntent(ctx, intent.ID, now, admitted, partial); markErr != nil {
					t.log.Warn("prewarm: failed to persist partial completion", "intent_id", intent.ID, "err", markErr)
				}
				t.emitFired(ctx, intent, admitted, "partial", partial)
				continue
			}
			if markErr := t.store.FailPrewarmIntent(ctx, intent.ID, now, admitErr.Error()); markErr != nil {
				t.log.Warn("prewarm: failed to persist failure", "intent_id", intent.ID, "err", markErr)
			}
			t.emitFired(ctx, intent, 0, "failed", admitErr.Error())
			t.log.Warn("prewarm: admission failed", "intent_id", intent.ID, "app_id", intent.AppID, "err", admitErr)
			continue
		}
		outcome := fmt.Sprintf("admitted:%d", admitted)
		if completeErr := t.store.CompletePrewarmIntent(ctx, intent.ID, now, admitted, outcome); completeErr != nil {
			if errors.Is(completeErr, context.Canceled) {
				return completeErr
			}
			t.log.Warn("prewarm: failed to persist completion", "intent_id", intent.ID, "err", completeErr)
		}
		t.emitFired(ctx, intent, admitted, "succeeded", outcome)
	}
	return nil
}

func (t *Trigger) emitFired(ctx context.Context, intent state.PrewarmIntent, admitted int, status, outcome string) {
	if t == nil || t.auditor == nil {
		return
	}
	var accountID *string
	if intent.AccountID != "" {
		id := intent.AccountID
		accountID = &id
	}
	t.auditor.Emit(ctx, "prewarm.fired", accountID, map[string]any{
		"intent_id":      intent.ID,
		"app_id":         intent.AppID,
		"count":          intent.Count,
		"admitted_count": admitted,
		"wake_at":        intent.WakeAt,
		"expires_at":     intent.ExpiresAt,
		"status":         status,
		"outcome":        outcome,
	})
}
