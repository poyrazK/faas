//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAppScalingPolicyObservationTracksLiveUpdatesWithoutDeployment(t *testing.T) {
	store, ctx := pgStore(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "scaling-policy-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "scaling-policy-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive,
		RAMMB: 256, MaxConcurrency: 4,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if app.ScalingPolicyRevision != 1 {
		t.Fatalf("created scaling policy revision = %d, want 1", app.ScalingPolicyRevision)
	}
	deployments, err := store.ListDeploymentsForApp(ctx, app.ID, 10, 0)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("deployments before policy update = %d, %v; want none", len(deployments), err)
	}

	if err := store.RecordAppScalingPolicyObserved(ctx, app.ID, "", 1); err != nil {
		t.Fatalf("record initial scheduler observation: %v", err)
	}
	status, err := store.GetAppScalingPolicyStatus(ctx, app.ID)
	if err != nil || status.DesiredRevision != 1 || status.ObservedRevision != 1 || status.ObservedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("initial scaling policy status = %+v, %v; want revision 1 freshly observed", status, err)
	}

	policy := &state.ScalingPolicy{MinInstances: 1, MaxInstances: 3, ScaleOutCooldownS: 5, ScaleInCooldownS: 60}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{ScalingPolicy: policy, SetScalingPolicy: true}); err != nil {
		t.Fatalf("update scaling policy: %v", err)
	}
	updated, err := store.AppByID(ctx, app.ID)
	if err != nil || updated.ScalingPolicyRevision != 2 {
		t.Fatalf("updated app scaling revision = %d, %v; want 2", updated.ScalingPolicyRevision, err)
	}
	status, err = store.GetAppScalingPolicyStatus(ctx, app.ID)
	if err != nil || status.DesiredRevision != 2 || status.ObservedRevision != 1 {
		t.Fatalf("status before scheduler reload = %+v, %v; want desired 2 observed 1", status, err)
	}
	if err := store.RecordAppScalingPolicyObserved(ctx, app.ID, "", 1); err == nil {
		t.Fatal("stale scheduler observation unexpectedly acknowledged revision 1")
	}
	if err := store.RecordAppScalingPolicyObserved(ctx, app.ID, "", 2); err != nil {
		t.Fatalf("record latest scheduler observation: %v", err)
	}
	status, err = store.GetAppScalingPolicyStatus(ctx, app.ID)
	if err != nil || status.DesiredRevision != 2 || status.ObservedRevision != 2 || status.ObservedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("status after scheduler reload = %+v, %v; want revision 2 freshly observed", status, err)
	}

	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: scalingIntPtr(512)}); err != nil {
		t.Fatalf("update unrelated resource setting: %v", err)
	}
	if revision, err := store.LatestAppScalingPolicyRevision(ctx, app.ID); err != nil || revision != 2 {
		t.Fatalf("revision after unrelated resource update = %d, %v; want 2", revision, err)
	}
	deployments, err = store.ListDeploymentsForApp(ctx, app.ID, 10, 0)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("deployments after policy update = %d, %v; policy changes must not create a deployment", len(deployments), err)
	}
}

func scalingIntPtr(value int) *int { return &value }
