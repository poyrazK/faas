package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testReleaseApprovals pins ADR-20261020085706's bookkeeping on both stores:
// the policy is one row per app that an update replaces in place, each person
// records one decision per release, decisions list oldest first, and removing
// the policy is final.
func testReleaseApprovals(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.ReleaseApprovalStore)
	if !ok {
		t.Fatalf("%T does not implement state.ReleaseApprovalStore", fx.Store)
	}
	acct, app := fx.Account.ID, fx.App.ID
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.ReleaseApprovalPolicy(fx.Ctx, app); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("policy before set err = %v, want ErrNotFound", err)
	}
	policy := state.ReleaseApprovalPolicy{AppID: app, AccountID: acct, RequiredApprovals: 2, AfterStage: 1, TimeoutMinutes: 1440,
		Approvers: state.ReleaseApproversAdmin, UpdatedBy: "user:conformance"}
	if _, err := store.SetReleaseApprovalPolicy(fx.Ctx, policy, now); err != nil {
		t.Fatal(err)
	}
	policy.RequiredApprovals, policy.Approvers, policy.AllowSelfApproval = 1, state.ReleaseApproversDeveloper, true
	updated, err := store.SetReleaseApprovalPolicy(fx.Ctx, policy, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ReleaseApprovalPolicy(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequiredApprovals != 1 || got.AfterStage != 1 || got.TimeoutMinutes != 1440 || got.Approvers != state.ReleaseApproversDeveloper ||
		!got.AllowSelfApproval || !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now.Add(time.Minute)) || got != updated {
		t.Fatalf("policy = %+v, updated = %+v", got, updated)
	}

	other, err := fx.Store.CreateAccount(fx.Ctx, "approvals-other-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	decisions := []state.ReleaseApproval{
		{AccountID: acct, AppID: app, DeploymentID: fx.Deployment.ID, ApproverAccountID: acct, Approver: "owner@example.com",
			ApproverRole: state.ReleaseApproverRolePersonal, Decision: state.ReleaseApprovalApprove, Comment: "canary looks clean"},
		{AccountID: acct, AppID: app, DeploymentID: fx.Deployment.ID, ApproverAccountID: other.ID, Approver: other.Email,
			ApproverRole: "admin", Decision: state.ReleaseApprovalReject},
	}
	for i, d := range decisions {
		if _, err := store.RecordReleaseApproval(fx.Ctx, d, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if _, err := store.RecordReleaseApproval(fx.Ctx, decisions[0], now.Add(time.Hour)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second decision by one person err = %v, want ErrConflict", err)
	}
	listed, err := store.ListReleaseApprovals(fx.Ctx, fx.Deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ApproverAccountID != acct || listed[0].Decision != state.ReleaseApprovalApprove ||
		listed[0].Comment != "canary looks clean" || listed[1].ApproverAccountID != other.ID || listed[1].Decision != state.ReleaseApprovalReject ||
		listed[1].ApproverRole != "admin" || listed[1].Comment != "" || !listed[1].CreatedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("listed = %+v", listed)
	}
	if listed, err := store.ListReleaseApprovals(fx.Ctx, uuid.NewString()); err != nil || len(listed) != 0 {
		t.Fatalf("unknown release = %+v, %v", listed, err)
	}

	removed, err := store.DeleteReleaseApprovalPolicy(fx.Ctx, app)
	if err != nil || removed.AppID != app {
		t.Fatalf("delete = %+v, %v", removed, err)
	}
	if _, err := store.DeleteReleaseApprovalPolicy(fx.Ctx, app); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}
