// Package canary (issue #976 / ADR-122 / SAFE-RELEASES-A) — runtime
// walker that advances canary ladder steps on a tick boundary.
//
// The catalog (pkg/api/canary) lives separately so pkg/api stays free
// of pkg/state import cycles; pkg/canary is the runtime twin — it
// imports pkg/api for the DeploymentResponse wire shape but does
// NOT import pkg/state (it talks to the store via a Store interface
// declared in this file, satisfied by pkg/state.Store's
// ListCanaryInFlight). The reason pkg/canary
// avoids the concrete pkg/state: cmd/meterd wires both packages and
// the meterd runtime must compile cleanly even when the meterd
// binary is cross-compiled to a machine without /dev/kvm (the
// Metal-test seam at deploy/lima).
//
// Once(ctx) is the single method cmd/meterd calls per tick. It walks
// state.ListCanaryInFlight, computes the next step on a wall-clock
// boundary, calls APID's atomic AdvanceCanary endpoint (apid-authoritative
// per CLAUDE.md ownership), which stamps traffic, canary state, terminal
// rollout state, and the deployment_audit row together.
// Per-row failures log + skip so a single broken canary never halts
// the tick.
package canary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	canarycatalog "github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Store is the slice of pkg/state.Store the runtime needs. Declared
// here (not imported) so pkg/canary stays import-cycle-free. The
// concrete *state.PgStore / *state.MemStore satisfy this surface via
// their own method sets.
type Store interface {
	ListCanaryInFlight(ctx context.Context) ([]CanaryRow, error)
}

// CanaryRow is the subset of state.Deployment the runtime reads.
// Mirrors the column names from migration 00480 so pkg/canary has
// zero dependency on pkg/state's package-level types. pkg/state.Store
// returns []state.Deployment; the meterd wiring wraps each row into
// this shape at the seam.
type CanaryRow struct {
	ID                string
	AppID             string
	CanaryPreset      string
	CanaryStep        int
	CanaryTotalSteps  int
	CanaryStepStarted time.Time
	RolloutState      string
	// CanaryStages is the jsonb-serialised custom ladder when
	// CanaryPreset == "custom" (SAFE-RELEASES production-leveling
	// Stream F, migrations/00487). nil/empty for catalog presets
	// (none/slow/balanced/aggressive/1-10-50-100) and for pre-PR
	// rows. The runtime reads this column only when
	// CanaryPreset == "custom"; the catalog resolution for the
	// other 5 names stays on canarycatalog.LookupPreset.
	CanaryStages json.RawMessage
}

// APIDClient is the slice of pkg/api.Client the runtime needs to
// shift traffic. Declared locally so pkg/canary can be tested with
// a fake client.
type APIDClient interface {
	AdvanceCanary(ctx context.Context, id string, expectedStep int) (api.CanaryAdvanceResponse, error)
}

// Progression drives the canary_progression tick. Construct via
// NewProgression in cmd/meterd; tests build one inline.
type Progression struct {
	Store Store
	APID  APIDClient
	Ops   *wire.OpsMetrics
	Log   *slog.Logger
	Now   func() time.Time
}

// NewProgression builds a Progression with nil-coerced Log / Now so
// a misconfigured daemon cannot silently skip audit emits (the
// runtime fails-soft via log.Warn; tests assert the audit emit).
func NewProgression(store Store, apid APIDClient, ops *wire.OpsMetrics, log *slog.Logger) *Progression {
	if log == nil {
		log = slog.Default()
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("meter_test")
	}
	return &Progression{
		Store: store,
		APID:  apid,
		Ops:   ops,
		Log:   log,
		Now:   time.Now,
	}
}

// Once walks every in-flight canary row and advances the step on a
// wall-clock boundary. Returns the number of rows advanced for
// observability; per-row failures log + skip so a single broken row
// never halts the tick.
//
// Logic per row:
//  1. Resolve pkg/api/canary.LookupPreset(CanaryPreset). If the
//     catalog doesn't recognise the name (a typo'd column write),
//     log warn + skip — never crash the daemon.
//  2. If canary_step >= canary_total_steps → row already complete;
//     skip (defensive — the predicate in ListCanaryInFlight should
//     have excluded it).
//  3. Compute next step = canary_step + 1. If next >= total, this
//     is the terminal step → APID completes the rollout in the same write.
//  4. Compare elapsed = Now() - canary_step_started_at against the
//     current stage's Duration. If < Duration → still on this step,
//     skip.
//  5. Call APID.AdvanceCanary(deployment_id, expected_step).
//     APID derives the next percentage and commits the CAS + traffic + audit
//     atomically. On error → log warn + skip.
//  6. Increment ops.CanaryProgressionAdvancedTotal().
func (p *Progression) Once(ctx context.Context) (Stats, error) {
	if p.Store == nil {
		return Stats{}, errors.New("canary: nil Store")
	}
	if p.APID == nil {
		return Stats{}, errors.New("canary: nil APID client")
	}
	rows, err := p.Store.ListCanaryInFlight(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("canary: list in-flight: %w", err)
	}
	stats := Stats{}
	if len(rows) == 0 {
		return stats, nil
	}
	now := p.now()
	for _, row := range rows {
		// SAFE-RELEASES production-leveling Stream F: when
		// CanaryPreset == "custom", the per-row stages live on
		// the deployment row's canary_stages jsonb column. The
		// catalog lookup returns a Preset with empty Stages
		// (catalog membership is what makes AllowedCanaryPreset
		// return true at write time); we rehydrate the actual
		// ladder from row.CanaryStages via canarycatalog.
		// LookupCustomPreset so the orchestrator's StageAt /
		// TotalSteps path is uniform with catalog presets.
		var preset canarycatalog.Preset
		var ok bool
		if row.CanaryPreset == "custom" {
			if len(row.CanaryStages) == 0 {
				p.Log.Warn("canary: custom preset with empty canary_stages; skipping",
					"deployment_id", row.ID)
				stats.SkippedUnknownPreset++
				continue
			}
			var customStages []canarycatalog.CustomStage
			if uerr := json.Unmarshal(row.CanaryStages, &customStages); uerr != nil || len(customStages) == 0 {
				p.Log.Warn("canary: custom preset stages unparseable; skipping",
					"deployment_id", row.ID, "err", uerr)
				stats.SkippedUnknownPreset++
				continue
			}
			custom, verr := canarycatalog.LookupCustomPreset(customStages)
			if verr != nil {
				p.Log.Warn("canary: custom preset validation failed; skipping",
					"deployment_id", row.ID, "err", verr)
				stats.SkippedUnknownPreset++
				continue
			}
			preset = custom
			ok = true
		} else {
			preset, ok = canarycatalog.LookupPreset(row.CanaryPreset)
			if !ok {
				p.Log.Warn("canary: unrecognised preset; skipping",
					"deployment_id", row.ID, "preset", row.CanaryPreset)
				stats.SkippedUnknownPreset++
				continue
			}
		}
		if row.CanaryStep < 0 || row.CanaryStep >= preset.TotalSteps() {
			p.Log.Warn("canary: step out of bounds; skipping",
				"deployment_id", row.ID, "step", row.CanaryStep, "total", preset.TotalSteps())
			stats.SkippedOutOfBounds++
			continue
		}
		currentStage, _ := preset.StageAt(row.CanaryStep)
		nextStep := row.CanaryStep + 1
		if nextStep >= preset.TotalSteps() {
			// Already on the terminal stage — should be unreachable
			// because the predicate excludes it, but defensive.
			stats.SkippedAlreadyTerminal++
			continue
		}
		nextStage, ok := preset.StageAt(nextStep)
		if !ok {
			stats.SkippedOutOfBounds++
			continue
		}
		// Wall-clock boundary: only advance when the current step's
		// Duration has elapsed since canary_step_started_at. Migration
		// 00517 (SAFE-RELEASES code-review hardening) locked the
		// column to NOT NULL DEFAULT NOW(), so the IsZero() branch
		// below is belt-and-braces for write paths that bypass the
		// schema default (e.g. a future code path that forgets to
		// stamp the timestamp; a rolled-back deployment row from a
		// pre-00517 backup). When we see zero, log + bump the
		// zero-timestamp counter so operators have visibility — and
		// still run the wall-clock check so behavior matches
		// pre-migration (elapsed = 56 years > Duration → advance).
		if row.CanaryStepStarted.IsZero() {
			p.Log.Warn("canary: canary_step_started_at is zero time; treating as 'advance now' (post-00517 schema default should prevent this)",
				"deployment_id", row.ID,
				"canary_preset", row.CanaryPreset,
				"canary_step", row.CanaryStep,
				"canary_total_steps", row.CanaryTotalSteps)
			if p.Ops != nil {
				p.Ops.CanaryProgressionZeroTimestampTotal()()
			}
		}
		elapsed := now.Sub(row.CanaryStepStarted)
		if elapsed < currentStage.Duration {
			stats.SkippedNotElapsed++
			continue
		}
		// Advance through APID's atomic state transition. The endpoint
		// derives nextStage.Percent from the persisted preset, so the
		// runtime cannot apply a stale or caller-invented traffic value.
		if _, err := p.APID.AdvanceCanary(ctx, row.ID, row.CanaryStep); err != nil {
			p.Log.Warn("canary: advance failed",
				"deployment_id", row.ID, "to_percent", nextStage.Percent, "err", err)
			stats.Errors++
			if p.Ops != nil {
				p.Ops.CanaryProgressionErrorsTotal("advance")()
			}
			continue
		}
		stats.Advanced++
		if p.Ops != nil {
			p.Ops.CanaryProgressionAdvancedTotal()()
		}
		// APID owns the terminal rollout_state transition; the next
		// list predicate excludes complete rows.
	}
	return stats, nil
}

func (p *Progression) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// Stats is the per-tick observation surface for ops + tests.
// Per-row counters are exclusive — a row is counted in exactly one
// bucket per tick (Errors is its own bucket; SkippedNotElapsed is
// distinct from SkippedUnknownPreset).
type Stats struct {
	Advanced               int
	Errors                 int
	SkippedUnknownPreset   int
	SkippedOutOfBounds     int
	SkippedAlreadyTerminal int
	SkippedNotElapsed      int
}
