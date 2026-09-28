package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
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

func TestPgStoreOrgActivityDomainMutationAtomic(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "org-activity-domain", "org-activity-domain")
	orgID, appUUID, actorID := uuid.New(), uuid.MustParse(appID), uuid.MustParse(accountID)
	const domain = "org-activity-domain.example.test"
	entry := state.OrgActivity{
		OrgID: orgID, Kind: "domain.added", ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "domain", ResourceID: domain, ResourceLabel: domain,
		AppID: &appUUID, SourceType: "domain.added", SourceID: "create-1",
		Data: []byte(`{"app_id":"` + appID + `"}`),
	}

	bad := entry
	bad.Data = []byte(`[]`)
	if _, _, err := s.CreateCustomDomainIfUnderQuotaWithActivity(ctx, domain, appID, "token", 10, 10, bad); err == nil {
		t.Fatal("domain create with invalid activity succeeded")
	}
	if _, err := s.DomainByName(ctx, domain); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("domain after rejected create/activity = %v, want ErrNotFound", err)
	}
	created, createOutboxID, err := s.CreateCustomDomainIfUnderQuotaWithActivity(ctx, domain, appID, "token", 10, 10, entry)
	if err != nil || created.Domain != domain || createOutboxID == 0 {
		t.Fatalf("transactional domain create = (%+v, %d, %v)", created, createOutboxID, err)
	}

	remove := entry
	remove.Kind = "domain.removed"
	remove.SourceType = "domain.removed"
	remove.SourceID = "delete-1"
	badRemove := remove
	badRemove.Data = []byte(`[]`)
	if _, err := s.DeleteCustomDomainWithActivity(ctx, domain, badRemove); err == nil {
		t.Fatal("domain delete with invalid activity succeeded")
	}
	if _, err := s.DomainByName(ctx, domain); err != nil {
		t.Fatalf("domain after rejected delete/activity = %v, want row to remain", err)
	}
	removeOutboxID, err := s.DeleteCustomDomainWithActivity(ctx, domain, remove)
	if err != nil || removeOutboxID == 0 {
		t.Fatalf("transactional domain delete = (%d, %v)", removeOutboxID, err)
	}
	if _, err := s.DeleteCustomDomainWithActivity(ctx, domain, remove); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("duplicate domain delete = %v, want ErrNotFound", err)
	}
	if _, err := s.DomainByName(ctx, domain); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("domain after delete = %v, want ErrNotFound", err)
	}

	for _, id := range []int64{createOutboxID, removeOutboxID} {
		if delivered, err := s.DeliverOrgActivityOutbox(ctx, id); err != nil || !delivered {
			t.Fatalf("deliver domain activity %d = (%v, %v), want true", id, delivered, err)
		}
	}
	rows, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("domain activity = (%#v, %v), want create and delete", rows, err)
	}
	kinds := map[string]bool{}
	for _, row := range rows {
		kinds[row.Kind] = true
	}
	if !kinds["domain.added"] || !kinds["domain.removed"] {
		t.Fatalf("domain activity kinds = %v, want add and remove", kinds)
	}
}

func TestPgStoreRollbackCompletionActivitySharesLiveTransition(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, targetID := seedLiveDeploy(t, s, ctx, "rollback-activity", "rollback-activity")
	current, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:current", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("create current deployment: %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, current.ID); err != nil {
		t.Fatalf("mark current deployment live: %v", err)
	}
	if _, err := s.PrepareDeploymentRollback(ctx, appID, targetID); err != nil {
		t.Fatalf("prepare rollback: %v", err)
	}

	orgID, appUUID, targetUUID, actorID := uuid.New(), uuid.MustParse(appID), uuid.MustParse(targetID), uuid.MustParse(accountID)
	request := state.OrgActivity{
		OrgID: orgID, Kind: "deploy.rollback_requested", ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "app", ResourceID: appID, ResourceLabel: "rollback-activity",
		AppID: &appUUID, DeploymentID: &targetUUID,
		Data:       []byte(`{"from":"` + current.ID + `","to":"` + targetID + `","phase":"readiness_requested"}`),
		SourceType: "rollback.requested", SourceID: "rollback-request-1",
	}
	requestOutboxID, err := s.EnqueueOrgActivityOutbox(ctx, request)
	if err != nil {
		t.Fatalf("enqueue rollback request: %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, targetID); err != nil {
		t.Fatalf("mark rollback target live: %v", err)
	}

	var completionOutboxID int64
	if err := pool.QueryRow(ctx, `
		select id from org_activity_outbox
		 where org_id = $1 and source_type = 'rollback.completed' and source_id = $2
	`, orgID, request.SourceID).Scan(&completionOutboxID); err != nil {
		t.Fatalf("find rollback completion outbox item: %v", err)
	}
	for _, id := range []int64{requestOutboxID, completionOutboxID} {
		if delivered, err := s.DeliverOrgActivityOutbox(ctx, id); err != nil || !delivered {
			t.Fatalf("deliver rollback activity %d = (%v, %v), want true", id, delivered, err)
		}
	}
	rows, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rollback timeline = (%#v, %v), want request and completion", rows, err)
	}
	kinds := map[string]state.OrgActivity{}
	for _, row := range rows {
		kinds[row.Kind] = row
	}
	completed, ok := kinds["deploy.rolled_back"]
	if !ok || completed.ActorAccountID == nil || *completed.ActorAccountID != actorID || completed.SourceID != request.SourceID {
		t.Fatalf("rollback completion = %+v, want original actor and request source", completed)
	}
	var completionData map[string]any
	if err := json.Unmarshal(completed.Data, &completionData); err != nil || completionData["phase"] != "completed" {
		t.Fatalf("rollback completion data = %s, %v; want completed", completed.Data, err)
	}
}

func TestPgStoreOrgActivityDeploymentMutationAtomic(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "org-activity-deploy-outbox", "org-activity-deploy-outbox")
	orgID, appUUID, actorID := uuid.New(), uuid.MustParse(appID), uuid.MustParse(accountID)
	entry := state.OrgActivity{
		OrgID: orgID, Kind: "deploy.requested", ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "app", ResourceID: appID, ResourceLabel: "org-activity-deploy-outbox",
		AppID: &appUUID, SourceType: "deployment.requested", Data: []byte(`{"source":"image","phase":"requested"}`),
	}

	bad := entry
	bad.Data = []byte(`[]`)
	badDeploymentID := uuid.NewString()
	if _, _, err := s.CreateDeploymentWithActivity(ctx, state.Deployment{ID: badDeploymentID, AppID: appID, Kind: state.DeploymentKindImage}, bad); err == nil {
		t.Fatal("deployment with invalid activity succeeded")
	}
	if _, err := s.DeploymentByID(ctx, badDeploymentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deployment after rejected transaction = %v, want ErrNotFound", err)
	}

	created, outboxID, err := s.CreateDeploymentWithActivity(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage}, entry)
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
	if err != nil || len(activity) != 1 || activity[0].Kind != "deploy.requested" || activity[0].SourceType != "deployment.requested" || activity[0].DeploymentID == nil || activity[0].DeploymentID.String() != created.ID {
		t.Fatalf("projected deployment activity = (%#v, %v)", activity, err)
	}
	if err := s.MarkDeploymentLive(ctx, created.ID); err != nil {
		t.Fatalf("mark activity deployment live: %v", err)
	}
	var liveOutboxID int64
	if err := pool.QueryRow(ctx, `select id from org_activity_outbox where org_id = $1 and source_type = 'deployment.live' and source_id = $2`, orgID, created.ID).Scan(&liveOutboxID); err != nil {
		t.Fatalf("find deployment-live outbox item: %v", err)
	}
	if delivered, err := s.DeliverOrgActivityOutbox(ctx, liveOutboxID); err != nil || !delivered {
		t.Fatalf("deliver deployment-live activity = (%v, %v), want true", delivered, err)
	}
	activity, err = s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(activity) != 2 {
		t.Fatalf("deployment request/live activity = (%#v, %v), want two rows", activity, err)
	}
	var sawLive bool
	for _, row := range activity {
		if row.Kind == "app.deployed" {
			sawLive = row.ActorAccountID != nil && *row.ActorAccountID == actorID
			var data map[string]any
			if err := json.Unmarshal(row.Data, &data); err != nil || data["phase"] != "live" {
				t.Fatalf("deployment-live activity data = %s, %v; want phase=live", row.Data, err)
			}
		}
	}
	if !sawLive {
		t.Fatalf("deployment-live activity missing original actor: %#v", activity)
	}

	buildDeployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage})
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

func TestPgStoreDeploymentFailureActivitySharesFailureTransition(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "org-activity-deploy-failed", "org-activity-deploy-failed")
	deployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	orgID, appUUID, actorID := uuid.New(), uuid.MustParse(appID), uuid.MustParse(accountID)
	deploymentUUID := uuid.MustParse(deployment.ID)
	request := state.OrgActivity{
		OrgID: orgID, Kind: "deploy.requested", ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "Bahadir",
		ResourceType: "app", ResourceID: appID, ResourceLabel: "org-activity-deploy-failed",
		AppID: &appUUID, DeploymentID: &deploymentUUID,
		Data:       []byte(`{"source":"image","phase":"requested"}`),
		SourceType: "deployment.requested", SourceID: deployment.ID,
	}
	if _, err := s.EnqueueOrgActivityOutbox(ctx, request); err != nil {
		t.Fatalf("enqueue deployment request: %v", err)
	}
	if _, err := s.SetDeploymentFailed(ctx, deployment.ID, "deployment_smoke_failed", "private stack detail"); err != nil {
		t.Fatalf("set deployment failed: %v", err)
	}
	var payload []byte
	if err := pool.QueryRow(ctx, `select activity from org_activity_outbox where org_id = $1 and source_type = 'deployment.failed' and source_id = $2`, orgID, deployment.ID).Scan(&payload); err != nil {
		t.Fatalf("find deployment-failed outbox item: %v", err)
	}
	var failed state.OrgActivity
	if err := json.Unmarshal(payload, &failed); err != nil {
		t.Fatalf("decode deployment-failed activity: %v", err)
	}
	if failed.Kind != "deploy.failed" || failed.ActorAccountID == nil || *failed.ActorAccountID != actorID || failed.ActorLabel != request.ActorLabel {
		t.Fatalf("deployment-failed activity = %+v; want failed event attributed to request actor", failed)
	}
	var data map[string]any
	if err := json.Unmarshal(failed.Data, &data); err != nil || data["phase"] != "failed" || data["error_code"] != "deployment_smoke_failed" || strings.Contains(string(failed.Data), "private stack detail") {
		t.Fatalf("deployment-failed data = %s, %v; want safe error_code without private message", failed.Data, err)
	}
}

func TestPgStoreCancelDeploymentWithActivityIsAtomic(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "org-activity-deploy-cancelled", "org-activity-deploy-cancelled")
	deployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, Status: state.DeployBuilding})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	orgID, appUUID, actorID, deploymentUUID := uuid.New(), uuid.MustParse(appID), uuid.MustParse(accountID), uuid.MustParse(deployment.ID)
	entry := state.OrgActivity{
		OrgID: orgID, Kind: "deploy.cancelled", ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "Bahadir",
		ResourceType: "app", ResourceID: appID, ResourceLabel: "org-activity-deploy-cancelled",
		AppID: &appUUID, DeploymentID: &deploymentUUID,
		Data:       []byte(`{"phase":"cancelled","reason":"user","previous_status":"building"}`),
		SourceType: "deployment.cancelled", SourceID: "cancel:" + deployment.ID,
	}
	bad := entry
	bad.Data = []byte(`[]`)
	if _, _, _, err := s.CancelDeploymentTxWithActivity(ctx, deployment.ID, accountID, state.CancelReasonUser, bad); err == nil {
		t.Fatal("cancellation with invalid activity succeeded")
	}
	if current, err := s.DeploymentByID(ctx, deployment.ID); err != nil || current.Status != deployment.Status {
		t.Fatalf("deployment after invalid activity = (%+v, %v); want unchanged status %q", current, err, deployment.Status)
	}
	var outboxRows int
	if err := pool.QueryRow(ctx, `select count(*) from org_activity_outbox where source_type = 'deployment.cancelled' and source_id = $1`, entry.SourceID).Scan(&outboxRows); err != nil || outboxRows != 0 {
		t.Fatalf("cancellation outbox rows after invalid activity = (%d, %v); want zero", outboxRows, err)
	}

	cancelled, _, outboxID, err := s.CancelDeploymentTxWithActivity(ctx, deployment.ID, accountID, state.CancelReasonUser, entry)
	if err != nil || cancelled.Status != state.DeployCancelled || outboxID == 0 {
		t.Fatalf("cancel with activity = (%+v, %d, %v); want cancelled and queued event", cancelled, outboxID, err)
	}
	if delivered, err := s.DeliverOrgActivityOutbox(ctx, outboxID); err != nil || !delivered {
		t.Fatalf("deliver cancellation activity = (%v, %v); want true", delivered, err)
	}
	rows, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].Kind != "deploy.cancelled" || rows[0].ActorLabel != entry.ActorLabel || rows[0].DeploymentID == nil || rows[0].DeploymentID.String() != deployment.ID {
		t.Fatalf("cancellation timeline = (%#v, %v); want one event attributed to the actor/deployment", rows, err)
	}
}

func TestPgStoreAppLifecycleActivityAtomic(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "app-lifecycle-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	actorID := uuid.MustParse(account.ID)
	slug := "activity-" + uuid.NewString()[:8]
	createdEntry := state.OrgActivity{
		Kind: "app.created", ActorType: state.OrgActivityActorUser, ActorAccountID: &actorID,
		ActorLabel: account.Email, ResourceType: "app", SourceType: "app.created",
		SourceID: "create-app-1", Data: []byte(`{"phase":"created","app_type":"app"}`),
	}
	badCreate := createdEntry
	badCreate.Data = []byte(`[]`)
	if _, _, err := s.CreateAppIfUnderQuotaWithActivity(ctx, state.App{AccountID: account.ID, Slug: slug}, api.MustLimitsFor(api.PlanPro), badCreate); err == nil {
		t.Fatal("create app with invalid activity succeeded")
	}
	if _, err := s.AppBySlug(ctx, slug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("app after rejected create/activity = %v, want ErrNotFound", err)
	}
	app, createdID, err := s.CreateAppIfUnderQuotaWithActivity(ctx, state.App{AccountID: account.ID, Slug: slug}, api.MustLimitsFor(api.PlanPro), createdEntry)
	if err != nil || createdID == 0 || app.OrgID == "" {
		t.Fatalf("transactional app create = (%+v, %d, %v), want app, org, and event", app, createdID, err)
	}

	deletedEntry := createdEntry
	deletedEntry.Kind = "app.deleted"
	deletedEntry.SourceType = "app.deleted"
	deletedEntry.SourceID = "delete-app-1"
	deletedEntry.Data = []byte(`{"phase":"deleted"}`)
	badDelete := deletedEntry
	badDelete.Data = []byte(`[]`)
	if _, _, err := s.ScheduleAppDeletionWithActivity(ctx, app.ID, time.Now().UTC().Add(time.Hour), badDelete); err == nil {
		t.Fatal("delete app with invalid activity succeeded")
	}
	if current, err := s.AppBySlug(ctx, app.Slug); err != nil || current.Status == state.AppDeleted {
		t.Fatalf("app after rejected delete/activity = (%+v, %v), want active", current, err)
	}
	deleted, deletedID, err := s.ScheduleAppDeletionWithActivity(ctx, app.ID, time.Now().UTC().Add(time.Hour), deletedEntry)
	if err != nil || deleted.Status != state.AppDeleted || deletedID == 0 {
		t.Fatalf("transactional app delete = (%+v, %d, %v), want deleted with event", deleted, deletedID, err)
	}
	duplicateDelete := deletedEntry
	duplicateDelete.SourceID = "delete-app-duplicate"
	if _, duplicateID, err := s.ScheduleAppDeletionWithActivity(ctx, app.ID, time.Now().UTC().Add(2*time.Hour), duplicateDelete); err != nil || duplicateID != 0 {
		t.Fatalf("repeat app delete = (%d, %v), want idempotent result without another event", duplicateID, err)
	}

	restoredEntry := createdEntry
	restoredEntry.Kind = "app.restored"
	restoredEntry.SourceType = "app.restored"
	restoredEntry.SourceID = "restore-app-1"
	restoredEntry.Data = []byte(`{"phase":"restored"}`)
	restored, restoredID, err := s.RestoreAppWithActivity(ctx, app.ID, api.MustLimitsFor(api.PlanScale), restoredEntry)
	if err != nil || restored.Status != state.AppActive || restoredID == 0 {
		t.Fatalf("transactional app restore = (%+v, %d, %v), want active with event", restored, restoredID, err)
	}

	for _, id := range []int64{createdID, deletedID, restoredID} {
		if delivered, err := s.DeliverOrgActivityOutbox(ctx, id); err != nil || !delivered {
			t.Fatalf("deliver app lifecycle event %d = (%v, %v), want true", id, delivered, err)
		}
	}
	rows, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(app.OrgID), Limit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatalf("app lifecycle timeline = (%#v, %v), want three events", rows, err)
	}
	kinds := map[string]bool{}
	for _, row := range rows {
		kinds[row.Kind] = true
		if row.ResourceLabel != app.Slug || row.AppID == nil || row.AppID.String() != app.ID {
			t.Errorf("app lifecycle identity = %+v, want slug %q and app id %q", row, app.Slug, app.ID)
		}
	}
	if !kinds["app.created"] || !kinds["app.deleted"] || !kinds["app.restored"] {
		t.Fatalf("app lifecycle kinds = %v, want create/delete/restore", kinds)
	}
}
