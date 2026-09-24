package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemStoreOrgActivityOutboxEnvMutationAndDelivery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	orgID, appID, actorID := uuid.New(), uuid.New(), uuid.New()
	entry := OrgActivity{
		OrgID: orgID, Kind: "env.set", ActorType: OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "environment_variable", ResourceID: "default:DATABASE_URL",
		ResourceLabel: "DATABASE_URL", AppID: &appID,
		SourceType: "env.set", SourceID: "request-1", Data: []byte(`{"scope":"default"}`),
	}

	bad := entry
	bad.Data = []byte(`"not an object"`)
	if _, err := store.UpsertAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "SHOULD_NOT_EXIST", "secret", bad); err == nil {
		t.Fatal("upsert with invalid activity succeeded")
	}
	if rows, err := store.ListAppEnvInScope(ctx, "account-1", appID.String(), "default"); err != nil || len(rows) != 0 {
		t.Fatalf("env rows after rejected transaction = %#v, err=%v; want no mutation", rows, err)
	}

	firstID, err := store.UpsertAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "DATABASE_URL", "secret", entry)
	if err != nil {
		t.Fatalf("transactional env upsert: %v", err)
	}
	duplicateID, err := store.UpsertAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "DATABASE_URL", "secret", entry)
	if err != nil || duplicateID != firstID {
		t.Fatalf("duplicate upsert id = %d, err=%v; want %d", duplicateID, err, firstID)
	}
	rows, err := store.ListAppEnvInScope(ctx, "account-1", appID.String(), "default")
	if err != nil || len(rows) != 1 || rows[0].Value != "secret" {
		t.Fatalf("env rows = %#v, err=%v", rows, err)
	}

	claimed, err := store.ClaimOrgActivityOutbox(ctx, "test-worker", time.Minute)
	if err != nil || claimed.ID != firstID || claimed.Attempts != 1 {
		t.Fatalf("claim = (%+v, %v), want id=%d attempts=1", claimed, err, firstID)
	}
	delivered, err := store.DeliverOrgActivityOutbox(ctx, firstID)
	if err != nil || !delivered {
		t.Fatalf("deliver = (%v, %v), want (true, nil)", delivered, err)
	}
	delivered, err = store.DeliverOrgActivityOutbox(ctx, firstID)
	if err != nil || delivered {
		t.Fatalf("redeliver = (%v, %v), want (false, nil)", delivered, err)
	}
	activity, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(activity) != 1 || activity[0].SourceID != entry.SourceID {
		t.Fatalf("activity = %#v, err=%v; want one projected event", activity, err)
	}

	deleteEntry := entry
	deleteEntry.Kind = "env.deleted"
	deleteEntry.SourceType = "env.deleted"
	deleteEntry.SourceID = "request-2"
	deleteID, err := store.DeleteAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "DATABASE_URL", deleteEntry)
	if err != nil || deleteID == 0 {
		t.Fatalf("transactional env delete = (%d, %v), want a queued event", deleteID, err)
	}
	if rows, err := store.ListAppEnvInScope(ctx, "account-1", appID.String(), "default"); err != nil || len(rows) != 0 {
		t.Fatalf("env rows after delete = %#v, err=%v; want no rows", rows, err)
	}
	if _, err := store.DeleteAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "DATABASE_URL", deleteEntry); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate delete = %v, want ErrNotFound", err)
	}

	externalEntry := deleteEntry
	externalEntry.Kind = "domain.tls_issued"
	externalEntry.ResourceType = "domain"
	externalEntry.ResourceID = "payments.example.com"
	externalEntry.ResourceLabel = "payments.example.com"
	externalEntry.SourceType = "certificate"
	externalEntry.SourceID = "payments.example.com:2030-01-01T00:00:00Z"
	externalID, err := store.EnqueueOrgActivityOutbox(ctx, externalEntry)
	if err != nil {
		t.Fatalf("enqueue external activity: %v", err)
	}
	duplicateExternalID, err := store.EnqueueOrgActivityOutbox(ctx, externalEntry)
	if err != nil || duplicateExternalID != externalID {
		t.Fatalf("duplicate external enqueue id = %d, err=%v; want %d", duplicateExternalID, err, externalID)
	}
	delivered, err = store.DeliverOrgActivityOutbox(ctx, externalID)
	if err != nil || !delivered {
		t.Fatalf("deliver external activity = (%v, %v), want (true, nil)", delivered, err)
	}
}

func TestMemStoreOrgActivityDeploymentMutationAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	orgID, appID, actorID := uuid.New(), uuid.New(), uuid.New()
	store.apps[appID.String()] = App{ID: appID.String(), AccountID: actorID.String(), Slug: "payments", Status: AppActive}
	entry := OrgActivity{
		OrgID: orgID, Kind: "app.deployed", ActorType: OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "app", ResourceID: appID.String(), ResourceLabel: "payments",
		AppID: &appID, SourceType: "deployment", Data: []byte(`{"source":"image"}`),
	}

	bad := entry
	bad.Data = []byte(`[]`)
	badDeploymentID := uuid.NewString()
	if _, _, err := store.CreateDeploymentWithActivity(ctx, Deployment{ID: badDeploymentID, AppID: appID.String()}, bad); err == nil {
		t.Fatal("deployment with invalid activity succeeded")
	}
	if _, err := store.DeploymentByID(ctx, badDeploymentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deployment after rejected transaction = %v, want ErrNotFound", err)
	}

	created, outboxID, err := store.CreateDeploymentWithActivity(ctx, Deployment{AppID: appID.String()}, entry)
	if err != nil || created.ID == "" || outboxID == 0 {
		t.Fatalf("transactional deployment = (%+v, %d, %v)", created, outboxID, err)
	}
	claimed, err := store.ClaimOrgActivityOutbox(ctx, "deployment-test", time.Minute)
	if err != nil || claimed.ID != outboxID || claimed.Activity.SourceID != created.ID ||
		claimed.Activity.DeploymentID == nil || *claimed.Activity.DeploymentID != uuid.MustParse(created.ID) {
		t.Fatalf("deployment activity claim = (%+v, %v), want source/deployment %s", claimed, err, created.ID)
	}
	if delivered, err := store.DeliverOrgActivityOutbox(ctx, outboxID); err != nil || !delivered {
		t.Fatalf("deployment activity delivery = (%v, %v), want true", delivered, err)
	}

	queuedDeployment, err := store.CreateDeployment(ctx, Deployment{AppID: appID.String()})
	if err != nil {
		t.Fatalf("create source deployment: %v", err)
	}
	bad.SourceID = queuedDeployment.ID
	badDeploymentUUID := uuid.MustParse(queuedDeployment.ID)
	bad.DeploymentID = &badDeploymentUUID
	if _, _, err := store.CreateBuildWithIDAndActivity(ctx, uuid.NewString(), queuedDeployment.ID, DeploymentKindTarball, 100, "build.log", bad); err == nil {
		t.Fatal("build with invalid activity succeeded")
	}
	unchanged, err := store.DeploymentByID(ctx, queuedDeployment.ID)
	if err != nil || unchanged.Status != DeployPending || unchanged.BuildID != "" {
		t.Fatalf("deployment after rejected build/activity transaction = (%+v, %v), want pending without build", unchanged, err)
	}

	activity := entry
	activity.SourceID = queuedDeployment.ID
	activityDeploymentUUID := uuid.MustParse(queuedDeployment.ID)
	activity.DeploymentID = &activityDeploymentUUID
	buildID := uuid.NewString()
	build, buildOutboxID, err := store.CreateBuildWithIDAndActivity(ctx, buildID, queuedDeployment.ID, DeploymentKindTarball, 100, "build.log", activity)
	if err != nil || build.ID != buildID || buildOutboxID == 0 {
		t.Fatalf("transactional build = (%+v, %d, %v)", build, buildOutboxID, err)
	}
	queuedActivity, err := store.ClaimOrgActivityOutbox(ctx, "deployment-test", time.Minute)
	if err != nil || queuedActivity.ID != buildOutboxID || queuedActivity.Activity.SourceID != queuedDeployment.ID {
		t.Fatalf("build activity claim = (%+v, %v), want source %s", queuedActivity, err, queuedDeployment.ID)
	}
}
