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
	withRuleCalls  int
	lastWithRuleID string
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

func (r *recordingAPID) PatchDeploymentsIdTraffic(_ context.Context, id string, percent int) (api.DeploymentResponse, error) {
	r.patchCalls++
	r.lastPatchID = id
	r.lastPatchPct = percent
	return api.DeploymentResponse{}, r.patchErr
}

func sampleRule() state.AlertRule {
	return state.AlertRule{
		ID:        "rule-1",
		Name:      "high-error-rate",
		AppID:     "my-app",
		Action:    state.AlertActionRollback,
		AccountID: "acct-1",
	}
}

// TestActionDispatcher_Rollback_RoutesToRollbackTo — action='rollback'
// fires Client.RollbackTo(slug, "") (the "any previous live"
// path). PatchDeploymentsIdTraffic is NOT called.
func TestActionDispatcher_Rollback_RoutesToRollbackTo(t *testing.T) {
	apid := &recordingAPID{}
	d := NewActionDispatcher(apid, discardLog(), "meterd:safedeploy")
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
	if apid.patchCalls != 0 {
		t.Errorf("PatchDeploymentsIdTraffic calls = %d; want 0 (rollback doesn't patch)", apid.patchCalls)
	}
}

// TestActionDispatcher_Demote_TodayNoOp — Commit 5's locked-in
// plan: demote/promote require a deployment_id on the rule's
// metadata which the current AlertRule schema doesn't carry.
// Today the dispatcher no-ops (warn-log) so the webhook fan-out
// is unaffected. A follow-up PR-D2 widens the schema; this test
// pins the placeholder behaviour.
func TestActionDispatcher_Demote_TodayNoOp(t *testing.T) {
	apid := &recordingAPID{}
	d := NewActionDispatcher(apid, discardLog(), "meterd:safedeploy")
	rule := sampleRule()
	rule.Action = state.AlertActionDemote

	if err := d.Execute(context.Background(), rule, 42.5, time.Now()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if apid.rollbackCalls != 0 || apid.patchCalls != 0 {
		t.Errorf("expected zero apid calls on demote no-op; got rollback=%d patch=%d",
			apid.rollbackCalls, apid.patchCalls)
	}
}

// TestActionDispatcher_Promote_TodayNoOp — same as demote — the
// deployment_id metadata column lands in PR-D2.
func TestActionDispatcher_Promote_TodayNoOp(t *testing.T) {
	apid := &recordingAPID{}
	d := NewActionDispatcher(apid, discardLog(), "meterd:safedeploy")
	rule := sampleRule()
	rule.Action = state.AlertActionPromote

	if err := d.Execute(context.Background(), rule, 42.5, time.Now()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if apid.rollbackCalls != 0 || apid.patchCalls != 0 {
		t.Errorf("expected zero apid calls on promote no-op; got rollback=%d patch=%d",
			apid.rollbackCalls, apid.patchCalls)
	}
}

// TestActionDispatcher_WebhookDefault_NoOp — action='webhook' is
// the legacy path; the dispatcher must NOT touch the apid
// client. The evaluator's runAction short-circuits before
// calling Execute in this case, but a defensive Execute keeps
// the contract honest.
func TestActionDispatcher_WebhookDefault_NoOp(t *testing.T) {
	apid := &recordingAPID{}
	d := NewActionDispatcher(apid, discardLog(), "meterd:safedeploy")
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
	d := NewActionDispatcher(apid, discardLog(), "meterd:safedeploy")
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

// TestActionDispatcher_AccountWideRule_NoOp — an account-wide
// rule (AppID == "") cannot be rolled back to a single
// deployment; the dispatcher treats it as a config-time skip.
func TestActionDispatcher_AccountWideRule_NoOp(t *testing.T) {
	apid := &recordingAPID{}
	d := NewActionDispatcher(apid, discardLog(), "meterd:safedeploy")
	rule := sampleRule()
	rule.AppID = "" // account-wide
	rule.Action = state.AlertActionRollback

	if err := d.Execute(context.Background(), rule, 42.5, time.Now()); err != nil {
		t.Errorf("Execute: got err=%v; want nil (account-wide rule is a config-time skip)", err)
	}
	if apid.rollbackCalls != 0 || apid.patchCalls != 0 {
		t.Errorf("expected zero apid calls on account-wide rule; got rollback=%d patch=%d",
			apid.rollbackCalls, apid.patchCalls)
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
	d := NewActionDispatcher(apid, discardLog(), "meterd:safedeploy")
	rule := sampleRule()
	rule.Action = state.AlertActionRollback

	err := d.Execute(context.Background(), rule, 42.5, time.Now())
	if err == nil {
		t.Errorf("Execute: err=nil; want non-nil on apid transport failure")
	}
}
