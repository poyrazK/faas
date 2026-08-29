// Package safedeploy (issue #976 / ADR-122 / SAFE-RELEASES) —
// orchestrator + action dispatcher for the "Safe Deploy" headline
// workflow. The package owns the rollout_state machine that walks
// deployments from pending → rolling_out → complete (or aborted),
// and implements the in-process action fan-out the alert
// evaluator surfaces when an alert rule's action column ≠
// 'webhook'.
//
// Two seams the wider system touches:
//
//  1. pkg/alerts.ActionExecutor (commit 4) — implemented here by
//     ActionDispatcher. The evaluator's fan-out calls
//     ActionDispatcher.Execute(ctx, rule, observed, at) when the
//     rule's action is 'rollback' / 'demote' / 'promote'. The
//     dispatcher routes to the appropriate pkg/api.Client call.
//     'webhook' and the empty-string default are intentionally
//     no-ops here — the legacy Dispatcher owns that path.
//
//  2. meterd's safedeploy tick (cmd/meterd/orchestrator_wiring.go)
//     — runs Orchestrator.Once(ctx) on a 30-second cadence (per
//     the locked-in plan decision; pkg/meter.DefaultSafeDeployInterval).
//     The orchestrator walks pending rollouts, flips
//     rollout_state, emits one deployment_audit row per
//     transition, and never writes deployment rows directly
//     (CLAUDE.md ownership rule: apid owns deployments.* and the
//     orchestrator goes through apid's PATCH endpoints).
//
// Concurrency: ActionDispatcher's HTTP client is connection-pooled
// and concurrency-safe — pkg/api.Client shares one http.Client per
// baseURL. The orchestrator is single-goroutine today (meterd's
// 9-tick Loop spawns one goroutine per tick family); a future
// per-rollout parallelism would land behind a per-deployment mutex
// (the Σ=100 traffic redistribution in pgstore.RedistributeTraffic
// already serialises per-deployment writes).
package safedeploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// APIDClient is the slice of pkg/api.Client the ActionDispatcher
// needs. Declared locally so pkg/safedeploy can be unit-tested with
// a recording fake (cmd/apid is the production implementation;
// tests pass a closure that captures the (id, percent) tuple).
type APIDClient interface {
	RollbackTo(ctx context.Context, slug, targetDeploymentID string) (api.DeploymentResponse, error)
	RollbackToWithRule(ctx context.Context, slug, targetDeploymentID, alertRuleID string) (api.DeploymentResponse, error)
	PatchDeploymentsIdTraffic(ctx context.Context, id string, percent int) (api.DeploymentResponse, error)
}

// ActionDispatcher is the production impl of pkg/alerts.ActionExecutor
// (the interface lives in pkg/alerts so the evaluator has zero
// dependency on pkg/safedeploy). It maps rule.Action ∈
// {rollback, demote, promote} to a single apid HTTP call.
//
// "webhook" and the empty-string default are intentionally not
// routed here — the legacy Dispatcher in pkg/alerts owns the
// webhook fan-out and is called before ActionDispatcher in the
// evaluator's flow. An unknown action is fail-soft (log warn +
// return nil so the evaluator's Stats.ActionFailed counter doesn't
// double-count a "the rule was wired but the action was bad"
// condition as a transport-level failure).
type ActionDispatcher struct {
	APID  APIDClient
	Log   *slog.Logger
	Now   func() time.Time
	Actor string // service-account sentinel stamped into deployment_audit
}

// NewActionDispatcher builds a dispatcher with nil-coerced
// dependencies. Actor is required (the audit row needs an actor
// string per the closed-set at migrations/00477_deployment_audit.sql).
func NewActionDispatcher(apid APIDClient, log *slog.Logger, actor string) *ActionDispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &ActionDispatcher{
		APID:  apid,
		Log:   log,
		Now:   time.Now,
		Actor: actor,
	}
}

// ErrActionDispatcherNoAPID is returned when the dispatcher is
// invoked with a nil APID client. This is a configuration error
// (cmd/meterd should never have wired the dispatcher without a
// token + base URL); surfacing it lets the evaluator's Stats
// counter distinguish "config bug" from "transport hiccup".
var ErrActionDispatcherNoAPID = errors.New("safedeploy: ActionDispatcher invoked with nil APID client")

// Execute implements pkg/alerts.ActionExecutor. The interface
// deliberately returns a single error so the evaluator's
// fail-soft log-warn path stays simple; the dispatcher itself
// chooses what counts as "failed" — an unknown action is treated
// as a config-time mistake (log warn, return nil) while a
// transport-level apid 5xx is treated as a transient failure
// (return the error so Stats.ActionFailed bumps).
//
// Pre-flight: a rule with no AppID is a config bug (the
// canary/rollback path requires an app to act on). Returns nil
// to keep the webhook fan-out path unaffected.
func (a *ActionDispatcher) Execute(ctx context.Context, rule state.AlertRule, observed float64, at time.Time) error {
	if a == nil || a.APID == nil {
		return ErrActionDispatcherNoAPID
	}
	if rule.AppID == "" {
		// Account-wide rules (no AppID) cannot be rolled back —
		// there's no single deployment to flip. Treat as a
		// config-time skip rather than a transport failure.
		a.Log.Warn("safedeploy: action on account-wide rule skipped (no AppID)",
			"rule", rule.ID, "name", rule.Name, "action", string(rule.Action))
		return nil
	}
	switch rule.Action {
	case state.AlertActionWebhook, "":
		// Legacy path — never reached in practice because the
		// evaluator's runAction short-circuits before calling
		// Execute for these, but defensive in case a future
		// caller forgets.
		return nil
	case state.AlertActionRollback:
		return a.doRollback(ctx, rule, observed, at)
	case state.AlertActionDemote:
		return a.doDemote(ctx, rule, observed, at)
	case state.AlertActionPromote:
		return a.doPromote(ctx, rule, observed, at)
	default:
		// Unknown action is a config-time mistake — the
		// validator at the alert rule create/update handler
		// should have caught it. Log warn + return nil so the
		// evaluator's Stats.ActionFailed counter (which signals
		// a transport failure) stays clean.
		a.Log.Warn("safedeploy: unknown action; no-op",
			"rule", rule.ID, "name", rule.Name, "action", string(rule.Action))
		return nil
	}
}

// doRollback flips the rule's app back to the previous live
// deployment via the legacy rollback endpoint (apid-authoritative;
// pkg/state stays out of the write path per CLAUDE.md ownership).
// "Previous live" means: the rule's AppID has a 'live' deployment
// row whose traffic_percent < 100 (the in-flight canary), and the
// rollback target is the row with traffic_percent=100. We don't
// re-resolve the target here — apid's POST /v1/apps/{slug}/rollback
// handler picks the most-recent superseded live row. Empty
// targetDeploymentID matches Client.RollbackTo's "any" path.
func (a *ActionDispatcher) doRollback(ctx context.Context, rule state.AlertRule, observed float64, at time.Time) error {
	slug := rule.AppID
	a.Log.Info("safedeploy: rollback triggered",
		"rule", rule.ID, "name", rule.Name, "slug", slug,
		"observed", observed, "fired_at", at.UTC().Format(time.RFC3339Nano))
	// SAFE-RELEASES-OBS PR-D (issue #976 / ADR-122): use
	// RollbackToWithRule so the resulting deployment_audit row
	// carries alert_rule_id=rule.ID. Operator's audit timeline
	// renders the rule as a clickable chip → /dashboard/alerts/{id}.
	// Passing rule.ID.String() (never empty) so the apid handler
	// stamps the column; empty would fall back to the legacy path.
	if _, err := a.APID.RollbackToWithRule(ctx, slug, "", rule.ID); err != nil {
		return fmt.Errorf("safedeploy: rollback %s: %w", slug, err)
	}
	return nil
}

// doDemote pins the in-flight canary deployment at 0% traffic.
// Pulls the latest 'live' deployment row for the rule's app and
// PATCHes its traffic_percent to 0; the apid handler does the
// Σ=100 redistribution (largest-remainder via pkg/state.RedistributeTraffic).
//
// Note: we don't know the deployment_id from the rule alone (the
// rule only carries AppID). The orchestrator's pkg/safedeploy
// passes the deployment_id into the alert context via a separate
// field on a future iteration; today this dispatches against
// the in-flight deployment the orchestrator is currently walking.
// To keep the ActionDispatcher surface narrow and the evaluator
// decoupled, this implementation talks to apid's PATCH endpoint
// with the deployment_id pulled from the rule's Metadata
// ("deployment_id") when present; otherwise it falls back to a
// no-op + warn log. The orchestrator's pkg/safedeploy ORchestrator
// owns the metadata stamping.
func (a *ActionDispatcher) doDemote(ctx context.Context, rule state.AlertRule, observed float64, at time.Time) error {
	depID := ruleDeploymentID(rule)
	if depID == "" {
		a.Log.Warn("safedeploy: demote requires deployment_id on rule metadata; no-op",
			"rule", rule.ID, "name", rule.Name)
		return nil
	}
	a.Log.Info("safedeploy: demote triggered",
		"rule", rule.ID, "name", rule.Name, "deployment_id", depID,
		"observed", observed, "fired_at", at.UTC().Format(time.RFC3339Nano))
	if _, err := a.APID.PatchDeploymentsIdTraffic(ctx, depID, 0); err != nil {
		return fmt.Errorf("safedeploy: demote %s: %w", depID, err)
	}
	return nil
}

// doPromote short-circuits the canary ladder to 100% traffic.
// Same depID resolution caveat as doDemote; the orchestrator
// stamps the deployment_id into the rule's metadata when the
// alert rule fires against a known canary step.
func (a *ActionDispatcher) doPromote(ctx context.Context, rule state.AlertRule, observed float64, at time.Time) error {
	depID := ruleDeploymentID(rule)
	if depID == "" {
		a.Log.Warn("safedeploy: promote requires deployment_id on rule metadata; no-op",
			"rule", rule.ID, "name", rule.Name)
		return nil
	}
	a.Log.Info("safedeploy: promote triggered",
		"rule", rule.ID, "name", rule.Name, "deployment_id", depID,
		"observed", observed, "fired_at", at.UTC().Format(time.RFC3339Nano))
	if _, err := a.APID.PatchDeploymentsIdTraffic(ctx, depID, 100); err != nil {
		return fmt.Errorf("safedeploy: promote %s: %w", depID, err)
	}
	return nil
}

// ruleDeploymentID extracts the deployment_id from the rule's
// metadata field. The AlertRule struct doesn't carry a typed
// Metadata field today — the orchestrator writes JSON into a
// custom column on alert_rules via the rule-creation handler
// (commit 2). For now this helper is a placeholder that returns
// ""; once the metadata column lands, this becomes a typed read
// from the metadata JSON.
//
// Why placeholder: the ActionDispatcher is designed for forward-
// compat with the metadata column. Today's evaluator-driven
// actions are limited to "rollback" (which only needs the slug,
// already on rule.AppID). Demote/promote actions against a
// specific in-flight canary deployment land in a follow-up
// PR-D2 — they require the rule to be scoped to the canary
// deployment, which is a richer alert-rule schema than what
// ships in Commit 2.
func ruleDeploymentID(rule state.AlertRule) string {
	return ""
}
