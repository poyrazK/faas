package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/state"
)

// releasePolicyStore is the read surface the default release policy needs.
// Stores that cannot answer it (narrow test fakes) leave deployments on the
// legacy immediate path.
type releasePolicyStore interface {
	LiveDeploymentForScope(ctx context.Context, appID, scope string) (state.Deployment, error)
}

// releasePolicyOutcome records what applyDefaultReleasePolicy did to a
// request, for logs and tests.
type releasePolicyOutcome string

const (
	releasePolicyNotApplied releasePolicyOutcome = ""
	// releasePolicySafe: the balanced canary and first-wake 5xx rollback
	// were filled in for every rollout field the request left unset.
	releasePolicySafe releasePolicyOutcome = "safe"
	// releasePolicySafeUnavailable: rollback was filled in, but the canary
	// worker lease was not ready, so traffic moves at once rather than the
	// deploy failing with safe_release_unavailable.
	releasePolicySafeUnavailable releasePolicyOutcome = "safe_canary_unavailable"
)

// applyDefaultReleasePolicy makes a health-gated rollout the default for a
// production release (ADR-911). It only fills fields the request left unset:
// an explicit canary, traffic_percent, or rollback_on_5xx always wins, and
// `canary: {preset: "none"}` opts one deploy out. It applies only to the
// default scope of a request-mode, non-preview app whose release_policy is
// not "immediate" and which already serves a live deployment; a first
// deploy has nothing to compare with or roll back to.
//
// Lookup failures fall back to the legacy immediate deploy: a default must
// never make a deploy fail where the explicit flag-free deploy succeeded, so
// each piece is only filled in when the plan gate that later validates it
// allows it.
func applyDefaultReleasePolicy(ctx context.Context, store any, log *slog.Logger, app state.App, plan api.Plan, req *api.CreateDeploymentRequest) releasePolicyOutcome {
	if !defaultReleasePolicyEligible(app, req) {
		return releasePolicyNotApplied
	}
	lookup, ok := store.(releasePolicyStore)
	if !ok {
		return releasePolicyNotApplied
	}
	if _, err := lookup.LiveDeploymentForScope(ctx, app.ID, api.DefaultEnvScope); err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			log.Warn("default release policy: live deployment lookup failed; deploying immediately",
				"app_id", app.ID, "err", err)
		}
		return releasePolicyNotApplied
	}
	if req.RollbackOn5xx == nil && plan.RollbackOn5xxAllowed() {
		enabled := true
		req.RollbackOn5xx = &enabled
	}
	if req.Canary != nil || req.TrafficPercent != nil || !plan.TrafficSplitAllowed() {
		return releasePolicySafe
	}
	if !safeReleaseWorkerReady(ctx, store, log) {
		log.Warn("default release policy: canary worker unavailable; deploying without a canary",
			"app_id", app.ID)
		return releasePolicySafeUnavailable
	}
	req.Canary = &api.CanaryPresetSpec{Preset: api.DefaultReleaseCanaryPreset}
	return releasePolicySafe
}

// safeReleaseDefaultEnabledEnv is the operator kill switch for the default
// (ADR-911). Unset or any value other than "false" keeps it on.
const safeReleaseDefaultEnabledEnv = "FAAS_SAFE_RELEASE_DEFAULT_ENABLED"

func defaultReleasePolicyEligible(app state.App, req *api.CreateDeploymentRequest) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(safeReleaseDefaultEnabledEnv)), "false") {
		return false
	}
	if app.Manifest.ReleasePolicy == api.ReleasePolicyImmediate || app.PreviewOfSlug != "" {
		return false
	}
	if mode := app.Manifest.ExecutionMode; mode != "" && mode != api.ExecutionModeRequest {
		// Services already roll out behind schedd's readiness gate; workers
		// and jobs serve no request traffic to compare.
		return false
	}
	if req.Environment != "" || (req.Scope != "" && req.Scope != api.DefaultEnvScope) {
		return false
	}
	return req.RollbackOn5xx == nil || (req.Canary == nil && req.TrafficPercent == nil)
}

func safeReleaseWorkerReady(ctx context.Context, store any, log *slog.Logger) bool {
	leaseStore, ok := store.(state.SafeReleaseWorkerLeaseStore)
	if !ok {
		return false
	}
	ready, err := leaseStore.SafeReleaseWorkerLeaseReady(ctx)
	if err != nil {
		log.Warn("default release policy: safe release lease read failed", "err", err)
		return false
	}
	return ready
}

// applyDefaultReleaseToEnqueue applies the default release policy to a
// GitHub App push, which carries no rollout fields of its own. A push to a
// named environment scope or to a service, worker, or job app is unchanged.
func applyDefaultReleaseToEnqueue(ctx context.Context, store any, log *slog.Logger, app state.App, plan api.Plan, scope string, p *apidsource.EnqueueParams) releasePolicyOutcome {
	req := &api.CreateDeploymentRequest{Scope: scope}
	outcome := applyDefaultReleasePolicy(ctx, store, log, app, plan, req)
	if outcome == releasePolicyNotApplied {
		return outcome
	}
	p.RollbackOn5xx = req.RollbackOn5xx != nil && *req.RollbackOn5xx
	if req.Canary == nil {
		return outcome
	}
	preset, ok := canary.LookupPreset(req.Canary.Preset)
	if !ok {
		return outcome
	}
	p.CanaryPreset = preset.Name
	p.CanaryTotalSteps = preset.TotalSteps()
	if first, ok := preset.StageAt(0); ok {
		p.TrafficPercent = first.Percent
	}
	now := time.Now().UTC()
	p.CanaryStepStartedAt = &now
	return outcome
}
