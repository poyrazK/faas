// cmd/meterd/canary_progression_ticks.go — canary_progression
// meterd tick (issue #976 / ADR-122 / SAFE-RELEASES-A).
//
// Pattern mirrors cmd/meterd/alert_presets_ticks.go::CertExpiryRefresherLoop
// — a free-function loop with ctx-cancellation, nil-coerced log/ops,
// and a default interval pulled from pkg/meter.DefaultCanaryEvalInterval.
// cmd/meterd/main.go wires this loop alongside the existing 8
// tick goroutines inside the Loop.WithCanaryProgression setter
// when FAAS_CANARY_PROGRESSION_TOKEN is set (the token is the
// apid-issued service-account credential the runtime needs to drive
// PatchDeploymentsIdTraffic).
//
// Returned error semantics: nil on every tick. The runtime's
// per-row failure model is log + skip (matches
// pkg/alerts/evaluator.go::Evaluator.RunOnce), so a transient
// apid 5xx or postgres hiccup never kills the daemon. The runtime
// emits canary_progression_advanced_total and
// canary_progression_errors_total{reason} via pkg/wire so the
// §12 dashboard panel can tripwire on a stalled advancement rate.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// CanaryProgressionParams is the param bundle for
// CanaryProgressionLoop. Store is required (the runtime reads
// ListCanaryInFlight); APID is required (the runtime calls
// PatchDeploymentsIdTraffic per step); Log, Ops, and Now are
// nil-coerced. Actor and Account are the service-account
// credentials stamped into the deployment_audit row.
type CanaryProgressionParams struct {
	Store    state.Store
	APID     *api.Client
	Log      *slog.Logger
	Ops      *wire.OpsMetrics
	Interval time.Duration
	Now      func() time.Time
	Actor    string
	Account  string
}

// CanaryProgressionLoop walks the canary_progression tick every
// interval. The runtime is package pkg/canary; this loop is a
// thin driver mirroring the cert-expiry + spend-aggregator
// precedent so the daemon shutdown / log / ops semantics stay
// uniform across the 9 goroutines (sample / quota / stripe /
// dunning / residency / alerts / probe / part / canary).
//
// The APID client is the LoadGenPattern: cmd/meterd constructs
// the apid-issued service-account token once at startup and
// threads the *api.Client through; a nil client disables the
// tick (cmd/meterd wires WithCanaryProgression only when the
// token is set).
func CanaryProgressionLoop(ctx context.Context, p CanaryProgressionParams) {
	if p.Store == nil || p.APID == nil {
		return
	}
	if p.Interval <= 0 {
		p.Interval = meter.DefaultCanaryEvalInterval
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	if p.Ops == nil {
		p.Ops = wire.NewOpsMetrics("meterd")
	}

	// Wrap the concrete pkg/state.Store behind the runtime's
	// narrow Store interface so pkg/canary stays free of the
	// pkg/state import cycle (the runtime is consumed by the
	// cross-compiled meterd binary, which compiles on machines
	// without /dev/kvm via the Lima test seam).
	progression := canary.NewProgression(
		&canaryStoreAdapter{store: p.Store},
		p.APID,
		p.Ops,
		p.Log,
		p.Actor,
		p.Account,
	)

	do := func() {
		walkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		stats, err := progression.Once(walkCtx)
		if err != nil {
			p.Log.Warn("meterd: canary_progression tick failed", "err", err)
			if c := p.Ops.CanaryProgressionErrorsTotal("list_in_flight"); c != nil {
				c.Inc()
			}
			return
		}
		p.Log.Info("meterd: canary_progression tick ok",
			"advanced", stats.Advanced,
			"errors", stats.Errors,
			"skipped_not_elapsed", stats.SkippedNotElapsed,
		)
	}
	do()
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			do()
		}
	}
}

// canaryStoreAdapter bridges pkg/state.Store to pkg/canary.Store.
// Lives in cmd/meterd (not pkg/canary) so pkg/canary stays free
// of the pkg/state import. The adapter is intentionally narrow:
// only ListCanaryInFlight + AppendDeploymentAudit — the runtime's
// full Store surface.
type canaryStoreAdapter struct {
	store state.Store
}

func (a *canaryStoreAdapter) ListCanaryInFlight(ctx context.Context) ([]canary.CanaryRow, error) {
	deps, err := a.store.ListCanaryInFlight(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]canary.CanaryRow, 0, len(deps))
	for _, d := range deps {
		// state.Deployment does not carry an AccountID column —
		// account ownership lives on the parent apps row. The audit
		// table's account_id column is nullable, so the runtime
		// emits the row with NULL account_id and the timeline
		// surfaces account ownership via the joined apps row at
		// read time. Filling this in would require a per-row
		// GetAppByID lookup, which adds a query per canary; the
		// cost is not worth it for a tick that walks ≤
		// ListCanaryInFlight rows every 30 s.
		out = append(out, canary.CanaryRow{
			ID:                d.ID,
			AppID:             d.AppID,
			AccountID:         "",
			CanaryPreset:      d.CanaryPreset,
			CanaryStep:        d.CanaryStep,
			CanaryTotalSteps:  d.CanaryTotalSteps,
			CanaryStepStarted: canaryPtrTime(d.CanaryStepStartedAt),
			RolloutState:      d.RolloutState,
			// SAFE-RELEASES Stream F: pass through the row's
			// canary_stages jsonb payload. Empty for catalog
			// presets (the orchestrator never reads it for those);
			// the canary Progression reads it ONLY when
			// CanaryPreset == "custom" to rehydrate the
			// synthesized Preset.
			CanaryStages: d.CanaryStages,
		})
	}
	return out, nil
}

func (a *canaryStoreAdapter) AppendDeploymentAudit(ctx context.Context, entry canary.AuditEntry) (int64, error) {
	depUUID, depErr := uuid.Parse(entry.DeploymentID)
	if depErr != nil {
		return 0, fmt.Errorf("canary: bad deployment_id %q: %w", entry.DeploymentID, depErr)
	}
	row := state.DeploymentAudit{
		DeploymentID: depUUID,
		Kind:         state.DeploymentAuditKind(entry.Kind),
		Actor:        entry.Actor,
		Data:         entry.Data,
	}
	if entry.AccountID != "" {
		acctUUID, acctErr := uuid.Parse(entry.AccountID)
		if acctErr != nil {
			return 0, fmt.Errorf("canary: bad account_id %q: %w", entry.AccountID, acctErr)
		}
		row.AccountID = &acctUUID
	}
	return a.store.AppendDeploymentAudit(ctx, row)
}

// canaryPtrTime is a small dereference helper so the adapter can
// pass *time.Time → time.Time without an if-chain per row.
func canaryPtrTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
