package conformance

import (
	"encoding/json"
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
	notify := uuid.NewString()
	policy.NotifyChannelIDs = []string{notify}
	updated, err := store.SetReleaseApprovalPolicy(fx.Ctx, policy, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ReleaseApprovalPolicy(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequiredApprovals != 1 || got.AfterStage != 1 || got.TimeoutMinutes != 1440 || got.Approvers != state.ReleaseApproversDeveloper ||
		!got.AllowSelfApproval || !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now.Add(time.Minute)) ||
		len(got.NotifyChannelIDs) != 1 || got.NotifyChannelIDs[0] != notify || len(updated.NotifyChannelIDs) != 1 || !updated.UpdatedAt.Equal(got.UpdatedAt) {
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

// testReleaseApprovalEvents pins the approval notification outbox: one event
// per release and kind, due events oldest first, retries move an event's next
// attempt, and a finished event is no longer pending.
func testReleaseApprovalEvents(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.ReleaseApprovalEventStore)
	if !ok {
		t.Fatalf("%T does not implement state.ReleaseApprovalEventStore", fx.Store)
	}
	now := time.Now().UTC().Truncate(time.Second)
	channel := uuid.NewString()
	event := func(kind string) state.ReleaseApprovalEvent {
		return state.ReleaseApprovalEvent{AccountID: fx.Account.ID, AppID: fx.App.ID, DeploymentID: fx.Deployment.ID, Event: kind,
			ChannelIDs: []string{channel}, Payload: []byte(`{"event":"` + kind + `","app":"conformance"}`)}
	}
	for i, kind := range []string{state.ReleaseApprovalEventRequested, state.ReleaseApprovalEventApproved} {
		recorded, err := store.EnqueueReleaseApprovalEvent(fx.Ctx, event(kind), now.Add(time.Duration(i)*time.Second))
		if err != nil || !recorded {
			t.Fatalf("enqueue %s = %v, %v", kind, recorded, err)
		}
	}
	if recorded, err := store.EnqueueReleaseApprovalEvent(fx.Ctx, event(state.ReleaseApprovalEventRequested), now.Add(time.Minute)); err != nil || recorded {
		t.Fatalf("second requested event = %v, %v; want ignored", recorded, err)
	}
	mine := func(events []state.ReleaseApprovalEvent) []state.ReleaseApprovalEvent {
		var out []state.ReleaseApprovalEvent
		for _, e := range events {
			if e.DeploymentID == fx.Deployment.ID {
				out = append(out, e)
			}
		}
		return out
	}
	due, err := store.DueReleaseApprovalEvents(fx.Ctx, now.Add(time.Second), 100)
	due = mine(due)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 || due[0].Event != state.ReleaseApprovalEventRequested || due[1].Event != state.ReleaseApprovalEventApproved ||
		due[0].State != state.ReleaseApprovalEventPending || len(due[0].ChannelIDs) != 1 || due[0].ChannelIDs[0] != channel {
		t.Fatalf("due = %+v", due)
	}
	var payload map[string]string
	if err := json.Unmarshal(due[0].Payload, &payload); err != nil || payload["app"] != "conformance" {
		t.Fatalf("payload = %s, %v", due[0].Payload, err)
	}
	if limited, err := store.DueReleaseApprovalEvents(fx.Ctx, now.Add(time.Second), 1); err != nil || len(limited) != 1 {
		t.Fatalf("limited = %+v, %v", limited, err)
	}
	retry := state.ReleaseApprovalEventAttempt{State: state.ReleaseApprovalEventPending, LastError: "slack returned 500", NextAttemptAt: now.Add(time.Hour), At: now}
	if err := store.FinishReleaseApprovalEvent(fx.Ctx, due[0].ID, retry); err != nil {
		t.Fatal(err)
	}
	delivered := state.ReleaseApprovalEventAttempt{State: state.ReleaseApprovalEventDelivered, NextAttemptAt: now, At: now.Add(2 * time.Second)}
	if err := store.FinishReleaseApprovalEvent(fx.Ctx, due[1].ID, delivered); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishReleaseApprovalEvent(fx.Ctx, due[1].ID, delivered); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("finishing a delivered event err = %v, want ErrNotFound", err)
	}
	if due, err := store.DueReleaseApprovalEvents(fx.Ctx, now.Add(time.Minute), 100); err != nil || len(mine(due)) != 0 {
		t.Fatalf("due before the retry = %+v, %v", due, err)
	}
	due, err = store.DueReleaseApprovalEvents(fx.Ctx, now.Add(2*time.Hour), 100)
	due = mine(due)
	if err != nil || len(due) != 1 || due[0].Attempts != 1 || due[0].LastError != "slack returned 500" || !due[0].NextAttemptAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("due after the retry = %+v, %v", due, err)
	}
}
