//go:build !no_pg

package state_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentCronJournalPG(t *testing.T) { store, _ := pgStore(t); testCronJournal(t, store) }
func TestDeploymentCronJournalPGConflicts(t *testing.T) {
	testCronConflicts(t, func(t *testing.T) state.Store { store, _ := pgStore(t); return store })
}
func TestDeploymentCronJournalPGConcurrency(t *testing.T) {
	store, _ := pgStore(t)
	testCronRace(t, store)
}
func TestDeploymentCronJournalPGOrdinaryEditRace(t *testing.T) {
	store, _ := pgStore(t)
	testCronOrdinaryEditRace(t, store)
}
func TestDeploymentCronJournalPGAccountQuotaRace(t *testing.T) {
	store, _ := pgStore(t)
	testCronAccountQuotaRace(t, store)
}
func TestDeploymentCronJournalPGCommandIdentityCollision(t *testing.T) {
	store, _ := pgStore(t)
	testCronIdentityCollision(t, store)
}

func TestDeploymentCronActivationPG(t *testing.T) {
	testCronActivation(t, func(t *testing.T) state.Store { store, _ := pgStore(t); return store })
}

// A company-owned app's account_id is the org's resource namespace, which has
// no accounts row (ADR-202610171908). Cron writes and deployment cron staging
// lock that namespace, so they work for company apps as well as personal ones.
func TestDeploymentCronJournalPGCompanyApp(t *testing.T) {
	store, _ := pgStore(t)
	ctx := t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "cron-company", Name: "Cron Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: org.ID, OrgID: org.ID, Slug: "cron-company-api"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateCron(ctx, app.ID, "* * * * *", "/a", true)
	if err != nil {
		t.Fatalf("create company cron: %v", err)
	}
	enabled := false
	if _, err = store.UpdateCron(ctx, created.ID, nil, nil, &enabled, nil); err != nil {
		t.Fatalf("update company cron: %v", err)
	}
	if err = store.DeleteCron(ctx, created.ID, app.ID); err != nil {
		t.Fatalf("delete company cron: %v", err)
	}
	var s state.DeploymentCronStagingStore = store
	snapshot, err := s.DeploymentCronStagingSnapshot(ctx, org.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	desired := []api.DeploymentCronConfig{{ID: uuid.NewString(), Schedule: "0 * * * *", Path: "/new", Enabled: true, Timezone: "UTC"}}
	staged, err := s.ApplyDeploymentCronStaging(ctx, app.ID, state.DeploymentCronStagingParams{AccountID: org.ID, Limits: api.MustLimitsFor(api.PlanPro), Request: api.DeploymentCronStagingRequest{SessionID: strings.Repeat("c", 32), Operation: "stage", ExpectedAppID: app.ID, ExpectedDeploymentID: snapshot.LatestDeploymentID, ExpectedRevision: snapshot.Revision, Before: snapshot.Crons, Desired: desired}})
	if err != nil || !staged.Changed {
		t.Fatalf("stage company crons=%+v err=%v", staged, err)
	}
}
