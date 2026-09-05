// action_test.go — pkg/safedeploy.ActionDispatcher unit tests.
// The ActionDispatcher is the production impl of
// pkg/alerts.ActionExecutor wired by cmd/meterd/main.go. The
// tests below cover the 4-row dispatch table
// (rollback / demote / promote / unknown) plus the nil-APID and
// account-wide-rule defensive branches.
package safedeploy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordingAPID is the in-memory fake for APIDClient. Tracks
// every RollbackTo / PatchDeploymentsIdTraffic call so tests
// can assert the dispatcher's decision tree.
type recordingAPID struct {
	rollbackErr error
	patchErr    error
	recoverErr  error

	rollbackCalls    int
	patchCalls       int
	lastPatchID      string
	lastPatchPct     int
	lastRollbackSlug string
	// SAFE-RELEASES-OBS PR-D: tracks RollbackToWithRule calls
	// (the new alert_rule_id-stamping variant). When the mock
	// observes a non-empty ruleID it bumps this counter so the
	// test can assert the dispatcher chose the rule-stamping
	// path over the legacy RollbackTo path.
	withRuleCalls      int
	lastWithRuleID     string
	recoverCalls       int
	lastRecoverSlug    string
	lastRecoverAction  string
	lastRecoverReason  string
	lastIdempotencyKey string
}

func (r *recordingAPID) RollbackTo(_ context.Context, slug, _ string) (api.DeploymentResponse, error) {
	r.rollbackCalls++
	r.lastRollbackSlug = slug
	return api.DeploymentResponse{}, r.rollbackErr
}

func (r *recordingAPID) RollbackToWithRule(_ context.Context, slug, _, alertRuleID string) (api.DeploymentResponse, error) {
	r.rollbackCalls++
	r.withRuleCalls++
	r.lastRollbackSlug = slug
	r.lastWithRuleID = alertRuleID
	return api.DeploymentResponse{}, r.rollbackErr
}

func (r *recordingAPID) RollbackToWithRuleAndIdempotencyKey(ctx context.Context, slug, targetDeploymentID, alertRuleID, idempotencyKey string) (api.DeploymentResponse, error) {
	r.lastIdempotencyKey = idempotencyKey
	return r.RollbackToWithRule(ctx, slug, targetDeploymentID, alertRuleID)
}

func (r *recordingAPID) PatchDeploymentsIdTraffic(_ context.Context, id string, percent int) (api.DeploymentResponse, error) {
	r.patchCalls++
	r.lastPatchID = id
	r.lastPatchPct = percent
	return api.DeploymentResponse{}, r.patchErr
}

func (r *recordingAPID) RecoverRollout(_ context.Context, slug, action, reason string) (api.RolloutTransitionResponse, error) {
	r.recoverCalls++
	r.lastRecoverSlug = slug
	r.lastRecoverAction = action
	r.lastRecoverReason = reason
	return api.RolloutTransitionResponse{}, r.recoverErr
}

func (r *recordingAPID) RecoverRolloutAndIdempotencyKey(ctx context.Context, slug, action, reason, idempotencyKey string) (api.RolloutTransitionResponse, error) {
	r.lastIdempotencyKey = idempotencyKey
	return r.RecoverRollout(ctx, slug, action, reason)
}

type recordingTargetResolver struct {
	app         state.App
	deployments []state.Deployment
	appErr      error
	listErr     error
}

func (r *recordingTargetResolver) AppByID(_ context.Context, _ string) (state.App, error) {
	return r.app, r.appErr
}

func (r *recordingTargetResolver) ListCanaryInFlight(_ context.Context) ([]state.Deployment, error) {
	return r.deployments, r.listErr
}

func activeTargetResolver() *recordingTargetResolver {
	return &recordingTargetResolver{
		app: state.App{ID: "app-id", Slug: "my-app"},
		deployments: []state.Deployment{{
			ID:               "deployment-1",
			AppID:            "app-id",
			Status:           state.DeployLive,
			CanaryTotalSteps: 4,
			CanaryStep:       1,
			RolloutState:     "rolling_out",
		}},
	}
}

func newActionDispatcher(apid APIDClient) *ActionDispatcher {
	return NewActionDispatcher(apid, discardLog(), "meterd:safedeploy").WithTargetResolver(activeTargetResolver())
}

func sampleRule() state.AlertRule {
	return state.AlertRule{
		ID:        "rule-1",
		Name:      "high-error-rate",
		AppID:     "app-id",
		Action:    state.AlertActionRollback,
		AccountID: "acct-1",
	}
}

// TestActionDispatcher_Rollback_RoutesToRollbackTo — action='rollback'
// resolves the app UUID to its slug and fires the rule-correlated rollback
// path. PatchDeploymentsIdTraffic is NOT called.
func TestActionDispatcher_Rollback_RoutesToRollbackTo(t *testing.T) {
	apid := &recordingAPID{}
	d := newActionDispatcher(apid)
	rule := sampleRule()
	rule.Action = state.AlertActionRollback

	if err := d.Execute(context.Background(), rule, 42.5, time.Now()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if apid.rollbackCalls != 1 {
		t.Errorf("RollbackTo calls = %d; want 1", apid.rollbackCalls)
	}
	if apid.lastRollbackSlug != "my-app" {
		t.Errorf("last RollbackTo slug = %q; want my-app", apid.lastRollbackSlug)
	}
	if apid.lastIdempotencyKey != "safedeploy/deployment-1/rollback" {
		t.Errorf("idempotency key = %q; want safedeploy/deployment-1/rollback", apid.lastIdempotencyKey)
	}
	if apid.patchCalls != 0 {
		t.Errorf("PatchDeploymentsIdTraffic calls = %d; want 0 (rollback doesn't patch)", apid.patchCalls)
	}
}

// TestActionDispatcher_Demote_UsesAtomicAbort — demotion aborts the canary
// through the atomic recovery endpoint instead of a racy traffic PATCH.
func TestActionDispatcher_Demote_UsesAtomicAbort(t *testing.T) {
	apid := &recordingAPID{}
	d := newActionDispatcher(apid)
	rule := sampleRule()
	rule.Action = state.AlertActionDemote

	if err := d.Execute(context.Background(), rule, 42.5, time.Now()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if apid.rollbackCalls != 0 || apid.patchCalls != 0 || apid.recoverCalls != 1 {
		t.Errorf("expected one atomic recovery and no legacy calls; got rollback=%d patch=%d recover=%d",
			apid.rollbackCalls, apid.patchCalls, apid.recoverCalls)
	}
	if apid.lastRecoverSlug != "my-app" || apid.lastRecoverAction != "abort" {
		t.Errorf("recovery target = (%q, %q); want (my-app, abort)", apid.lastRecoverSlug, apid.lastRecoverAction)
	}
	if apid.lastIdempotencyKey != "safedeploy/deployment-1/demote" {
		t.Errorf("idempotency key = %q; want safedeploy/deployment-1/demote", apid.lastIdempotencyKey)
	}
}

// TestActionDispatcher_Promote_UsesAtomicRecovery — promotion must go
// through the app-scoped recovery endpoint so traffic and rollout state
// change in one transaction.
func TestActionDispatcher_Promote_UsesAtomicRecovery(t *testing.T) {
	apid := &recordingAPID{}
	d := newActionDispatcher(apid)
	rule := sampleRule()
	rule.Action = state.AlertActionPromote

	if err := d.Execute(context.Background(), rule, 42.5, time.Now()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if apid.rollbackCalls != 0 || apid.patchCalls != 0 || apid.recoverCalls != 1 {
		t.Errorf("expected one atomic recovery and no legacy calls; got rollback=%d patch=%d recover=%d",
			apid.rollbackCalls, apid.patchCalls, apid.recoverCalls)
	}
	if apid.lastRecoverSlug != "my-app" || apid.lastRecoverAction != "promote" {
		t.Errorf("recovery target = (%q, %q); want (my-app, promote)", apid.lastRecoverSlug, apid.lastRecoverAction)
	}
	if apid.lastIdempotencyKey != "safedeploy/deployment-1/promote" {
		t.Errorf("idempotency key = %q; want safedeploy/deployment-1/promote", apid.lastIdempotencyKey)
	}
}

// TestActionDispatcher_WebhookDefault_NoOp — action='webhook' is
// the legacy path; the dispatcher must NOT touch the apid
// client. The evaluator's runAction short-circuits before
// calling Execute in this case, but a defensive Execute keeps
// the contract honest.
func TestActionDispatcher_WebhookDefault_NoOp(t *testing.T) {
	apid := &recordingAPID{}
	d := newActionDispatcher(apid)
	rule := sampleRule()
	rule.Action = state.AlertActionWebhook

	if err := d.Execute(context.Background(), rule, 42.5, time.Now()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if apid.rollbackCalls != 0 || apid.patchCalls != 0 {
		t.Errorf("expected zero apid calls on webhook default; got rollback=%d patch=%d",
			apid.rollbackCalls, apid.patchCalls)
	}
}

// TestActionDispatcher_UnknownAction_NoOpNoError — an unknown
// action (typo, schema drift) is a config-time mistake. The
// dispatcher warn-logs and returns nil so the evaluator's
// Stats.ActionFailed counter (which signals a transport
// failure) stays clean.
func TestActionDispatcher_UnknownAction_NoOpNoError(t *testing.T) {
	apid := &recordingAPID{}
	d := newActionDispatcher(apid)
	rule := sampleRule()
	rule.Action = state.AlertAction("rollbac") // typo

	if err := d.Execute(context.Background(), rule, 42.5, time.Now()); err != nil {
		t.Errorf("Execute: got err=%v; want nil (unknown action is a config bug, not transport failure)", err)
	}
	if apid.rollbackCalls != 0 || apid.patchCalls != 0 {
		t.Errorf("expected zero apid calls on unknown action; got rollback=%d patch=%d",
			apid.rollbackCalls, apid.patchCalls)
	}
}

// TestActionDispatcher_AccountWideRule_FailsClosed — an account-wide rule
// cannot be mapped to one rollout target.
func TestActionDispatcher_AccountWideRule_FailsClosed(t *testing.T) {
	apid := &recordingAPID{}
	d := newActionDispatcher(apid)
	rule := sampleRule()
	rule.AppID = "" // account-wide
	rule.Action = state.AlertActionRollback

	if err := d.Execute(context.Background(), rule, 42.5, time.Now()); !errors.Is(err, ErrActionTargetUnavailable) {
		t.Errorf("Execute: err=%v; want ErrActionTargetUnavailable", err)
	}
	if apid.rollbackCalls != 0 || apid.patchCalls != 0 {
		t.Errorf("expected zero apid calls on account-wide rule; got rollback=%d patch=%d",
			apid.rollbackCalls, apid.patchCalls)
	}
}

func TestActionDispatcher_NoActiveCanary_FailsClosed(t *testing.T) {
	apid := &recordingAPID{}
	targets := activeTargetResolver()
	targets.deployments = nil
	d := NewActionDispatcher(apid, discardLog(), "meterd:safedeploy").WithTargetResolver(targets)
	rule := sampleRule()
	rule.Action = state.AlertActionRollback

	err := d.Execute(context.Background(), rule, 42.5, time.Now())
	if !errors.Is(err, ErrActionTargetUnavailable) {
		t.Fatalf("Execute: err=%v; want ErrActionTargetUnavailable", err)
	}
	if apid.rollbackCalls != 0 {
		t.Errorf("RollbackTo calls = %d; want 0", apid.rollbackCalls)
	}
}

func TestActionDispatcher_MultipleActiveCanaries_FailsClosed(t *testing.T) {
	apid := &recordingAPID{}
	targets := activeTargetResolver()
	targets.deployments = append(targets.deployments, state.Deployment{
		ID:               "deployment-2",
		AppID:            "app-id",
		Status:           state.DeployLive,
		CanaryStep:       2,
		CanaryTotalSteps: 4,
		RolloutState:     "rolling_out",
	})
	d := NewActionDispatcher(apid, discardLog(), "meterd:safedeploy").WithTargetResolver(targets)
	rule := sampleRule()
	rule.Action = state.AlertActionPromote

	err := d.Execute(context.Background(), rule, 42.5, time.Now())
	if !errors.Is(err, ErrActionTargetAmbiguous) {
		t.Fatalf("Execute: err=%v; want ErrActionTargetAmbiguous", err)
	}
	if apid.recoverCalls != 0 {
		t.Errorf("RecoverRollout calls = %d; want 0", apid.recoverCalls)
	}
}

// TestActionDispatcher_NilAPID_ReturnsError — calling Execute
// without an APID client surfaces ErrActionDispatcherNoAPID so
// the meterd log book call out the misconfiguration loudly.
func TestActionDispatcher_NilAPID_ReturnsError(t *testing.T) {
	d := &ActionDispatcher{APID: nil, Log: discardLog(), Actor: "meterd:safedeploy"}
	rule := sampleRule()
	rule.Action = state.AlertActionRollback

	err := d.Execute(context.Background(), rule, 42.5, time.Now())
	if !errors.Is(err, ErrActionDispatcherNoAPID) {
		t.Errorf("Execute: err=%v; want ErrActionDispatcherNoAPID", err)
	}
}

// TestActionDispatcher_RollbackTransportError_Propagates — an
// apid 5xx on RollbackTo must surface as a non-nil error so the
// evaluator's Stats.ActionFailed counter bumps.
func TestActionDispatcher_RollbackTransportError_Propagates(t *testing.T) {
	apid := &recordingAPID{rollbackErr: errors.New("synthetic apid 503")}
	d := newActionDispatcher(apid)
	rule := sampleRule()
	rule.Action = state.AlertActionRollback

	err := d.Execute(context.Background(), rule, 42.5, time.Now())
	if err == nil {
		t.Errorf("Execute: err=nil; want non-nil on apid transport failure")
	}
}
