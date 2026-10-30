package state_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A deploy's manifest crons activate once the deployment serves, against the
// plan of the app's billing subject (ADR-202610171904). A company app keeps
// its creator's account_id, and activation read the creator's personal plan:
// a Free member's deploy of a Pro company's app never activated its crons.
//
// adr: 202610171904
func TestMemCompanyCronActivationUsesTheCompanysPlan(t *testing.T) {
	companyCronActivationContract(t, state.NewMemStore(), nil)
}

// companyCronActivationContract deploys a Pro company's app, created by a
// Free member, with one manifest cron. adopt adopts the organization's billing
// for stores that do not adopt a Free organization on create.
func companyCronActivationContract(t *testing.T, store state.Store, adopt func(orgID string)) {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	member, err := store.CreateAccount(ctx, "free-member-"+suffix+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "activation-co-" + suffix, Name: "Activation Co", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if adopt != nil {
		adopt(org.ID)
	}
	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: member.ID, OrgID: org.ID, Slug: "company-" + suffix, Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	staging := store.(state.DeploymentCronStagingStore)
	snapshot, err := staging.DeploymentCronStagingSnapshot(ctx, member.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	desired := []api.DeploymentCronConfig{{ID: uuid.NewString(), Schedule: "*/5 * * * *", Path: "/report", Enabled: true, Timezone: "UTC"}}
	// apid admits the deploy against the company's plan.
	p := state.DeploymentCronStagingParams{AccountID: member.ID, Limits: api.MustLimitsFor(api.PlanPro), Request: api.DeploymentCronStagingRequest{
		SessionID: strings.ReplaceAll(uuid.NewString(), "-", ""), Operation: "prepare", ExpectedAppID: app.ID,
		ExpectedDeploymentID: snapshot.LatestDeploymentID, ExpectedRevision: snapshot.Revision, Before: snapshot.Crons, Desired: desired}}
	if prepared, err := staging.ApplyDeploymentCronStaging(ctx, app.ID, p); err != nil || prepared.Receipt.Status != "prepared" {
		t.Fatalf("prepare = %+v, %v", prepared, err)
	}
	dep, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(ctx, p.Request.SessionID),
		state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	changes, err := store.(state.DeploymentCronActivationStore).ReconcileDeploymentCrons(ctx)
	if err != nil || len(changes) != 1 {
		t.Fatalf("activations = %+v, %v; want the company app's", changes, err)
	}
	after, err := staging.DeploymentCronStagingSnapshot(ctx, member.ID, app.ID)
	if err != nil || !api.DeploymentCronsEqual(after.Crons, desired) {
		t.Fatalf("active crons = %+v, %v; want the deploy's", after.Crons, err)
	}
}
