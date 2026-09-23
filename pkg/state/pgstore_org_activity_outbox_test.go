package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreOrgActivityOutboxEnvMutationAndDelivery(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "org-activity-outbox", "org-activity-outbox")
	orgID, appUUID, actorID := uuid.New(), uuid.MustParse(appID), uuid.MustParse(accountID)
	entry := state.OrgActivity{
		OrgID: orgID, Kind: "env.set", ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "environment_variable", ResourceID: "default:DATABASE_URL",
		ResourceLabel: "DATABASE_URL", AppID: &appUUID,
		SourceType: "env.set", SourceID: "request-1", Data: []byte(`{"scope":"default"}`),
	}

	if _, err := s.UpsertAppEnvInScopeWithActivity(ctx, accountID, appID, "default", "SHOULD_NOT_EXIST", "secret", state.OrgActivity{}); err == nil {
		t.Fatal("upsert with invalid activity succeeded")
	}
	rows, err := s.ListAppEnvInScope(ctx, accountID, appID, "default")
	if err != nil || len(rows) != 0 {
		t.Fatalf("env rows after rejected transaction = %#v, err=%v; want no mutation", rows, err)
	}

	id, err := s.UpsertAppEnvInScopeWithActivity(ctx, accountID, appID, "default", "DATABASE_URL", "secret", entry)
	if err != nil {
		t.Fatalf("transactional env upsert: %v", err)
	}
	duplicateID, err := s.UpsertAppEnvInScopeWithActivity(ctx, accountID, appID, "default", "DATABASE_URL", "secret", entry)
	if err != nil || duplicateID != id {
		t.Fatalf("duplicate upsert id = %d, err=%v; want %d", duplicateID, err, id)
	}
	rows, err = s.ListAppEnvInScope(ctx, accountID, appID, "default")
	if err != nil || len(rows) != 1 || rows[0].Value != "secret" {
		t.Fatalf("env rows = %#v, err=%v", rows, err)
	}

	claimed, err := s.ClaimOrgActivityOutbox(ctx, "test-worker", time.Minute)
	if err != nil || claimed.ID != id || claimed.Attempts != 1 {
		t.Fatalf("claim = (%+v, %v), want id=%d attempts=1", claimed, err, id)
	}
	delivered, err := s.DeliverOrgActivityOutbox(ctx, id)
	if err != nil || !delivered {
		t.Fatalf("deliver = (%v, %v), want (true, nil)", delivered, err)
	}
	delivered, err = s.DeliverOrgActivityOutbox(ctx, id)
	if err != nil || delivered {
		t.Fatalf("redeliver = (%v, %v), want (false, nil)", delivered, err)
	}
	activity, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(activity) != 1 || activity[0].SourceID != entry.SourceID {
		t.Fatalf("activity = %#v, err=%v; want one projected event", activity, err)
	}

	deleteEntry := entry
	deleteEntry.Kind = "env.deleted"
	deleteEntry.SourceType = "env.deleted"
	deleteEntry.SourceID = "request-2"
	deleteID, err := s.DeleteAppEnvInScopeWithActivity(ctx, accountID, appID, "default", "DATABASE_URL", deleteEntry)
	if err != nil || deleteID == 0 {
		t.Fatalf("transactional env delete = (%d, %v), want a queued event", deleteID, err)
	}
	if _, err := s.DeleteAppEnvInScopeWithActivity(ctx, accountID, appID, "default", "DATABASE_URL", deleteEntry); err == nil {
		t.Fatal("delete of missing env unexpectedly succeeded")
	}
	rows, err = s.ListAppEnvInScope(ctx, accountID, appID, "default")
	if err != nil || len(rows) != 0 {
		t.Fatalf("env rows after delete = %#v, err=%v; want no rows", rows, err)
	}

	var stateName string
	var lastError *string
	if err := pool.QueryRow(ctx, `select state, last_error from org_activity_outbox where id = $1`, id).Scan(&stateName, &lastError); err != nil {
		t.Fatalf("read outbox state: %v", err)
	}
	if stateName != "delivered" || lastError != nil {
		t.Fatalf("outbox state = (%q, %v), want delivered with no error", stateName, lastError)
	}
	var payload []byte
	if err := pool.QueryRow(ctx, `select activity from org_activity_outbox where id = $1`, id).Scan(&payload); err != nil {
		t.Fatalf("read durable payload: %v", err)
	}
	if !json.Valid(payload) || strings.Contains(string(payload), "secret") {
		t.Fatalf("durable activity payload is not safe JSON: %s", payload)
	}

	externalEntry := deleteEntry
	externalEntry.Kind = "domain.tls_issued"
	externalEntry.ResourceType = "domain"
	externalEntry.ResourceID = "payments.example.com"
	externalEntry.ResourceLabel = "payments.example.com"
	externalEntry.SourceType = "certificate"
	externalEntry.SourceID = "payments.example.com:2030-01-01T00:00:00Z"
	externalID, err := s.EnqueueOrgActivityOutbox(ctx, externalEntry)
	if err != nil {
		t.Fatalf("enqueue external activity: %v", err)
	}
	duplicateExternalID, err := s.EnqueueOrgActivityOutbox(ctx, externalEntry)
	if err != nil || duplicateExternalID != externalID {
		t.Fatalf("duplicate external enqueue id = %d, err=%v; want %d", duplicateExternalID, err, externalID)
	}
	delivered, err = s.DeliverOrgActivityOutbox(ctx, externalID)
	if err != nil || !delivered {
		t.Fatalf("deliver external activity = (%v, %v), want (true, nil)", delivered, err)
	}
	activity, err = s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(activity) != 2 {
		t.Fatalf("activity after external delivery = %#v, err=%v; want two projected events", activity, err)
	}
}

func TestPgStoreOrgActivityDeploymentMutationAtomic(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "org-activity-deploy-outbox", "org-activity-deploy-outbox")
	orgID, appUUID, actorID := uuid.New(), uuid.MustParse(appID), uuid.MustParse(accountID)
	entry := state.OrgActivity{
		OrgID: orgID, Kind: "app.deployed", ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "app", ResourceID: appID, ResourceLabel: "org-activity-deploy-outbox",
		AppID: &appUUID, SourceType: "deployment", Data: []byte(`{"source":"image"}`),
	}

	bad := entry
	bad.Data = []byte(`[]`)
	badDeploymentID := uuid.NewString()
	if _, _, err := s.CreateDeploymentWithActivity(ctx, state.Deployment{ID: badDeploymentID, AppID: appID}, bad); err == nil {
		t.Fatal("deployment with invalid activity succeeded")
	}
	if _, err := s.DeploymentByID(ctx, badDeploymentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deployment after rejected transaction = %v, want ErrNotFound", err)
	}

	created, outboxID, err := s.CreateDeploymentWithActivity(ctx, state.Deployment{AppID: appID}, entry)
	if err != nil || created.ID == "" || outboxID == 0 {
		t.Fatalf("transactional deployment = (%+v, %d, %v)", created, outboxID, err)
	}
	claimed, err := s.ClaimOrgActivityOutbox(ctx, "deployment-test", time.Minute)
	if err != nil || claimed.ID != outboxID || claimed.Activity.SourceID != created.ID ||
		claimed.Activity.DeploymentID == nil || claimed.Activity.DeploymentID.String() != created.ID {
		t.Fatalf("deployment activity claim = (%+v, %v), want source/deployment %s", claimed, err, created.ID)
	}
	if delivered, err := s.DeliverOrgActivityOutbox(ctx, outboxID); err != nil || !delivered {
		t.Fatalf("deployment activity delivery = (%v, %v), want true", delivered, err)
	}
	activity, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(activity) != 1 || activity[0].DeploymentID == nil || activity[0].DeploymentID.String() != created.ID {
		t.Fatalf("projected deployment activity = (%#v, %v)", activity, err)
	}

	buildDeployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID})
	if err != nil {
		t.Fatalf("create source deployment: %v", err)
	}
	buildEntry := entry
	buildDeploymentUUID := uuid.MustParse(buildDeployment.ID)
	buildEntry.DeploymentID = &buildDeploymentUUID
	buildEntry.SourceID = buildDeployment.ID
	buildID := uuid.NewString()
	build, buildOutboxID, err := s.CreateBuildWithIDAndActivity(ctx, buildID, buildDeployment.ID, state.DeploymentKindTarball, 100, "build.log", buildEntry)
	if err != nil || build.ID != buildID || buildOutboxID == 0 {
		t.Fatalf("transactional build = (%+v, %d, %v)", build, buildOutboxID, err)
	}
	queued, err := s.ClaimOrgActivityOutbox(ctx, "deployment-test", time.Minute)
	if err != nil || queued.ID != buildOutboxID || queued.Activity.SourceID != buildDeployment.ID {
		t.Fatalf("build activity claim = (%+v, %v), want source %s", queued, err, buildDeployment.ID)
	}
}
