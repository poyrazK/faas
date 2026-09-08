// cmd/meterd/canary_progression_ticks.go — canary_progression
// adapter (issue #976 / ADR-122 / SAFE-RELEASES-A).
//
// The tick driver lives on meter.Loop and is wired once from main.go. This
// file keeps only the narrow state.Store adapter needed by pkg/canary, so a
// second free-function ticker cannot accidentally run alongside the loop.
package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

// canaryStoreAdapter bridges pkg/state.Store to pkg/canary.Store. It lives in
// cmd/meterd so pkg/canary stays free of the pkg/state import cycle.
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
		app, appErr := a.store.AppByID(ctx, d.AppID)
		if appErr != nil {
			return nil, appErr
		}
		out = append(out, canary.CanaryRow{
			ID:                d.ID,
			AppID:             d.AppID,
			AppSlug:           app.Slug,
			CanaryPreset:      d.CanaryPreset,
			CanaryStep:        d.CanaryStep,
			CanaryTotalSteps:  d.CanaryTotalSteps,
			CanaryStepStarted: canaryPtrTime(d.CanaryStepStartedAt),
			RolloutState:      d.RolloutState,
			CanaryStages:      d.CanaryStages,
		})
	}
	return out, nil
}

// FiringSafeReleaseSignals is the promotion-side health gate. Alert rules
// with an explicit rollback or demote action are authoritative signals that a
// canary should not advance while the alert evaluator is still handling the
// regression. Notification-only webhook rules remain non-blocking.
func (a *canaryStoreAdapter) FiringSafeReleaseSignals(ctx context.Context, appID string) ([]canary.HealthSignal, error) {
	rules, err := a.store.ListEnabledAlertRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]canary.HealthSignal, 0)
	for _, rule := range rules {
		if rule.AppID != appID || rule.State != state.AlertStateFiring {
			continue
		}
		if rule.Action != state.AlertActionRollback && rule.Action != state.AlertActionDemote {
			continue
		}
		out = append(out, canary.HealthSignal{
			RuleID:      rule.ID,
			RuleName:    rule.Name,
			Metric:      string(rule.Metric),
			Action:      string(rule.Action),
			LastFiredAt: rule.LastFiredAt,
		})
	}
	return out, nil
}

// MirrorSummaryForDeployment aggregates every enabled mirror rule whose
// source is the in-flight deployment. A stage condition is app-scoped but a
// deployment can have more than one mirror target, so the decision sees the
// sum of all comparison ledgers in the requested window.
func (a *canaryStoreAdapter) MirrorSummaryForDeployment(ctx context.Context, appID, deploymentID string, since time.Time) (canary.MirrorSummary, error) {
	rules, err := a.store.ListMirrorRules(ctx, appID)
	if err != nil {
		return canary.MirrorSummary{}, err
	}
	var out canary.MirrorSummary
	for _, rule := range rules {
		if !rule.Enabled || rule.SourceDeploymentID != deploymentID {
			continue
		}
		summary, err := a.store.MirrorSummary(ctx, rule.ID, since)
		if err != nil {
			return canary.MirrorSummary{}, err
		}
		out.TotalInvocations += summary.TotalInvocations
		out.StatusDiffCount += summary.StatusDiffCount
		out.SchemaDiffCount += summary.SchemaDiffCount
		out.BodyDiffCount += summary.BodyDiffCount
		out.CrashCount += summary.CrashCount
	}
	return out, nil
}

func canaryPtrTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
