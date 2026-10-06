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
//  1. pkg/alerts.ActionExecutor and ClaimedActionExecutor — implemented
//     here by ActionDispatcher. The evaluator passes the committed
//     alert fire to ExecuteClaimed. Rollback addresses APID's durable
//     outbox; demote and promote use Execute to resolve a canary and
//     call the corresponding APID action.
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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// APIDClient is the slice of pkg/api.Client the ActionDispatcher
// needs. Declared locally so pkg/safedeploy can be unit-tested with
// a recording fake (cmd/apid is the production implementation).
type APIDClient interface {
	RollbackTo(ctx context.Context, slug, targetDeploymentID string) (api.DeploymentResponse, error)
	RollbackToWithRule(ctx context.Context, slug, targetDeploymentID, alertRuleID string) (api.DeploymentResponse, error)
}

type rolloutRecoveryClient interface {
	RecoverRollout(ctx context.Context, slug, action, reason string) (api.RolloutTransitionResponse, error)
}

type keyedSafeDeployClient interface {
	RollbackToWithRuleAndIdempotencyKey(ctx context.Context, slug, targetDeploymentID, alertRuleID, idempotencyKey string) (api.DeploymentResponse, error)
	RecoverRolloutAndIdempotencyKey(ctx context.Context, slug, action, reason, idempotencyKey string) (api.RolloutTransitionResponse, error)
}

// RolloutTargetResolver resolves an alert rule's app UUID to the one active
// canary rollout that an automated action is allowed to mutate.
type RolloutTargetResolver interface {
	AppByID(context.Context, string) (state.App, error)
	ListCanaryInFlight(context.Context) ([]state.Deployment, error)
}

// ActionDispatcher is the production impl of pkg/alerts.ActionExecutor
// (the interface lives in pkg/alerts so the evaluator has zero
// dependency on pkg/safedeploy). It maps rule.Action ∈
// {rollback, demote, promote} to APID actions. Rollback forwards the durable
// fire receipt; APID also processes it when the callback is lost. Demote maps
// to atomic rollout abort so the canary leaves traffic and progression.
//
// "webhook" and the empty-string default are intentionally not
// routed here — the legacy Dispatcher in pkg/alerts owns the
// webhook fan-out and is called before ActionDispatcher in the
// evaluator's flow. An unknown action is fail-soft (log warn +
// return nil so the evaluator's Stats.ActionFailed counter doesn't
// double-count a "the rule was wired but the action was bad"
// condition as a transport-level failure).
type ActionDispatcher struct {
	APID    APIDClient
	Targets RolloutTargetResolver
	Log     *slog.Logger
	Now     func() time.Time
	Actor   string // service-account sentinel stamped into deployment_audit
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

func (a *ActionDispatcher) WithTargetResolver(targets RolloutTargetResolver) *ActionDispatcher {
	a.Targets = targets
	return a
}

type rolloutActionTarget struct {
	App        state.App
	Deployment state.Deployment
}

// ErrActionDispatcherNoAPID is returned when the dispatcher is
// invoked with a nil APID client. This is a configuration error
// (cmd/meterd should never have wired the dispatcher without a
// token + base URL); surfacing it lets the evaluator's Stats
// counter distinguish "config bug" from "transport hiccup".
var ErrActionDispatcherNoAPID = errors.New("safedeploy: ActionDispatcher invoked with nil APID client")

var ErrActionTargetUnavailable = errors.New("safedeploy: automated action target is unavailable")

var ErrActionTargetAmbiguous = errors.New("safedeploy: automated action target is ambiguous")

// Execute implements pkg/alerts.ActionExecutor. The interface
// deliberately returns a single error so the evaluator's
// fail-soft log-warn path stays simple; the dispatcher itself
// chooses what counts as "failed" — an unknown action is treated
// as a config-time mistake (log warn, return nil) while a
// transport-level apid 5xx is treated as a transient failure
// (return the error so Stats.ActionFailed bumps).
//
// Demote and promote must resolve exactly one active canary. Account-wide
// rules and stale/ambiguous targets fail closed. Rollback requires
// ExecuteClaimed so it uses the pair selected by the committed alert fire.
func (a *ActionDispatcher) Execute(ctx context.Context, rule state.AlertRule, observed float64, at time.Time) error {
	if a == nil || a.APID == nil {
		return ErrActionDispatcherNoAPID
	}
	switch rule.Action {
	case state.AlertActionWebhook, "":
		// Legacy path — never reached in practice because the
		// evaluator's runAction short-circuits before calling
		// Execute for these, but defensive in case a future
		// caller forgets.
		return nil
	case state.AlertActionRollback:
		return fmt.Errorf("%w: rollback requires a durable alert fire", ErrActionTargetUnavailable)
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

type durableAlertRollbackClient interface {
	ProcessAlertRollback(context.Context, string) (api.AlertRollback, error)
}

// ExecuteClaimed uses the fire outbox already committed by ClaimAlertFire.
// Retries always address that fire, never a newly selected canary.
func (a *ActionDispatcher) ExecuteClaimed(ctx context.Context, rule state.AlertRule, fireID string, observed float64, at time.Time) error {
	if rule.Action != state.AlertActionRollback {
		return a.Execute(ctx, rule, observed, at)
	}
	if a == nil || a.APID == nil {
		return ErrActionDispatcherNoAPID
	}
	client, ok := a.APID.(durableAlertRollbackClient)
	if !ok || fireID == "" {
		return fmt.Errorf("%w: durable alert rollback is unavailable", ErrActionTargetUnavailable)
	}
	receipt, err := client.ProcessAlertRollback(ctx, fireID)
	if err != nil {
		return fmt.Errorf("safedeploy: process alert rollback: %w", err)
	}
	receiptID, receiptErr := uuid.Parse(receipt.ID)
	requestedID, requestedErr := uuid.Parse(fireID)
	sameFire := receipt.ID == fireID || receiptErr == nil && requestedErr == nil && receiptID == requestedID
	if !sameFire || receipt.RuleID != rule.ID || receipt.AppID != rule.AppID || receipt.AccountID != rule.AccountID {
		return fmt.Errorf("%w: mismatched alert rollback receipt", ErrActionTargetUnavailable)
	}
	if receipt.Status == "failed" {
		return fmt.Errorf("safedeploy: alert rollback failed: %s", receipt.Code)
	}
	switch receipt.Status {
	case "pending", "blocked", "complete":
		return nil
	default:
		return fmt.Errorf("%w: unknown alert rollback status", ErrActionTargetUnavailable)
	}
}

// doDemote aborts the active canary through the same atomic recovery
// transaction as manual rollout recovery. Aborting is the durable demotion
// state: it removes the canary from future progression and redistributes its
// traffic to the remaining live revisions.
func (a *ActionDispatcher) doDemote(ctx context.Context, rule state.AlertRule, observed float64, at time.Time) error {
	target, err := a.resolveTarget(ctx, rule)
	if err != nil {
		return err
	}
	recovery, ok := a.APID.(rolloutRecoveryClient)
	if !ok {
		return fmt.Errorf("%w: APID client has no atomic rollout recovery", ErrActionTargetUnavailable)
	}
	reason := safeDeployActionReason(rule, observed)
	if handled, err := recoverExactCanaryAbort(ctx, a.APID, a.Targets, target.Deployment, reason, safeDeployActionKey(target.Deployment, string(rule.Action))); handled {
		if err != nil {
			return fmt.Errorf("safedeploy: demote %s: %w", target.App.Slug, err)
		}
		return nil
	}
	if keyed, ok := a.APID.(keyedSafeDeployClient); ok {
		_, err = keyed.RecoverRolloutAndIdempotencyKey(ctx, target.App.Slug, "abort", reason, safeDeployActionKey(target.Deployment, string(rule.Action)))
	} else {
		_, err = recovery.RecoverRollout(ctx, target.App.Slug, "abort", reason)
	}
	if err != nil {
		return fmt.Errorf("safedeploy: demote %s: %w", target.App.Slug, err)
	}
	return nil
}

// doPromote short-circuits the canary ladder through apid's atomic recovery
// endpoint, which updates traffic, rollout state, and audit together.
func (a *ActionDispatcher) doPromote(ctx context.Context, rule state.AlertRule, observed float64, at time.Time) error {
	target, err := a.resolveTarget(ctx, rule)
	if err != nil {
		return err
	}
	a.Log.Info("safedeploy: promote triggered",
		"rule", rule.ID, "name", rule.Name, "slug", target.App.Slug,
		"deployment_id", target.Deployment.ID,
		"observed", observed, "fired_at", at.UTC().Format(time.RFC3339Nano))
	recovery, ok := a.APID.(rolloutRecoveryClient)
	if !ok {
		return fmt.Errorf("%w: APID client has no atomic rollout recovery", ErrActionTargetUnavailable)
	}
	reason := safeDeployActionReason(rule, observed)
	if keyed, ok := a.APID.(keyedSafeDeployClient); ok {
		_, err = keyed.RecoverRolloutAndIdempotencyKey(ctx, target.App.Slug, "promote", reason, safeDeployActionKey(target.Deployment, string(rule.Action)))
	} else {
		_, err = recovery.RecoverRollout(ctx, target.App.Slug, "promote", reason)
	}
	if err != nil {
		return fmt.Errorf("safedeploy: promote %s: %w", target.App.Slug, err)
	}
	return nil
}

func (a *ActionDispatcher) resolveTarget(ctx context.Context, rule state.AlertRule) (rolloutActionTarget, error) {
	if rule.AppID == "" {
		return rolloutActionTarget{}, fmt.Errorf("%w: rule %q is account-wide", ErrActionTargetUnavailable, rule.ID)
	}
	if a.Targets == nil {
		return rolloutActionTarget{}, fmt.Errorf("%w: no rollout target resolver is configured", ErrActionTargetUnavailable)
	}
	app, err := a.Targets.AppByID(ctx, rule.AppID)
	if err != nil {
		return rolloutActionTarget{}, fmt.Errorf("%w: resolve app %q: %w", ErrActionTargetUnavailable, rule.AppID, err)
	}
	if app.ID != rule.AppID || app.Slug == "" || rule.AccountID != "" && app.AccountID != rule.AccountID {
		return rolloutActionTarget{}, fmt.Errorf("%w: app %q has no usable slug", ErrActionTargetUnavailable, rule.AppID)
	}
	deployments, err := a.Targets.ListCanaryInFlight(ctx)
	if err != nil {
		return rolloutActionTarget{}, fmt.Errorf("%w: list active canaries: %w", ErrActionTargetUnavailable, err)
	}
	var target state.Deployment
	for _, deployment := range deployments {
		if deployment.AppID != rule.AppID || !isActiveCanary(deployment) {
			continue
		}
		if target.ID != "" {
			return rolloutActionTarget{}, fmt.Errorf("%w: app %q has multiple active canaries", ErrActionTargetAmbiguous, rule.AppID)
		}
		target = deployment
	}
	if target.ID == "" {
		return rolloutActionTarget{}, fmt.Errorf("%w: app %q has no active canary", ErrActionTargetUnavailable, rule.AppID)
	}
	return rolloutActionTarget{App: app, Deployment: target}, nil
}

func isActiveCanary(deployment state.Deployment) bool {
	if deployment.Status != state.DeployLive || deployment.CanaryTotalSteps <= 0 || deployment.CanaryStep >= deployment.CanaryTotalSteps {
		return false
	}
	return deployment.RolloutState == "pending" || deployment.RolloutState == "rolling_out"
}

func safeDeployActionReason(rule state.AlertRule, observed float64) string {
	return fmt.Sprintf("alert %q fired for %s (observed %.6g)", rule.Name, rule.Metric, observed)
}

func safeDeployActionKey(deployment state.Deployment, action string) string {
	return fmt.Sprintf("safedeploy/%s/%s", deployment.ID, action)
}
