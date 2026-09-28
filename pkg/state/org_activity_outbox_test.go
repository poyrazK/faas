package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
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
		OrgID: orgID, Kind: "deploy.requested", ActorType: OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "app", ResourceID: appID.String(), ResourceLabel: "payments",
		AppID: &appID, SourceType: "deployment.requested", Data: []byte(`{"source":"image","phase":"requested"}`),
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

func TestMemStoreDeploymentOutcomeActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, tc := range []struct {
		name      string
		outcome   string
		wantKind  string
		wantPhase string
	}{
		{name: "live", outcome: "live", wantKind: "app.deployed", wantPhase: "live"},
		{name: "failed", outcome: "failed", wantKind: "deploy.failed", wantPhase: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := NewMemStore()
			orgID, appID, actorID := uuid.New(), uuid.New(), uuid.New()
			store.apps[appID.String()] = App{ID: appID.String(), AccountID: actorID.String(), Slug: "payments", Status: AppActive}
			entry := OrgActivity{
				OrgID: orgID, Kind: "deploy.requested", ActorType: OrgActivityActorUser,
				ActorAccountID: &actorID, ActorLabel: "Bahadir",
				ResourceType: "app", ResourceID: appID.String(), ResourceLabel: "payments",
				AppID: &appID, SourceType: "deployment.requested",
				Data: []byte(`{"source":"image","phase":"requested"}`),
			}
			deployment, requestOutboxID, err := store.CreateDeploymentWithActivity(ctx, Deployment{AppID: appID.String()}, entry)
			if err != nil {
				t.Fatalf("create deployment with activity: %v", err)
			}
			if tc.outcome == "live" {
				if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
					t.Fatalf("mark deployment live: %v", err)
				}
			} else if _, err := store.SetDeploymentFailed(ctx, deployment.ID, "deployment_smoke_failed", "private stack detail"); err != nil {
				t.Fatalf("set deployment failed: %v", err)
			}

			store.mu.Lock()
			defer store.mu.Unlock()
			var outcomeActivity OrgActivity
			for id, row := range store.orgActivityOutbox {
				if id != requestOutboxID && row.Activity.Kind == tc.wantKind {
					outcomeActivity = row.Activity
					break
				}
			}
			if outcomeActivity.Kind != tc.wantKind {
				t.Fatalf("outcome outbox activity = %+v, want kind %q", outcomeActivity, tc.wantKind)
			}
			if outcomeActivity.ActorAccountID == nil || *outcomeActivity.ActorAccountID != actorID || outcomeActivity.ActorLabel != entry.ActorLabel {
				t.Fatalf("outcome actor = %+v, want original actor %+v", outcomeActivity, entry)
			}
			var data map[string]any
			if err := json.Unmarshal(outcomeActivity.Data, &data); err != nil {
				t.Fatalf("decode outcome data %s: %v", outcomeActivity.Data, err)
			}
			if data["phase"] != tc.wantPhase {
				t.Fatalf("outcome phase = %v, want %q", data["phase"], tc.wantPhase)
			}
			if tc.outcome == "failed" && (data["error_code"] != "deployment_smoke_failed" || strings.Contains(string(outcomeActivity.Data), "private stack detail")) {
				t.Fatalf("failure data = %s; want safe error_code only, without private message", outcomeActivity.Data)
			}
		})
	}
}

func TestMemStoreCancelDeploymentWithActivityIsAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	orgID, appID, actorID := uuid.New(), uuid.New(), uuid.New()
	store.apps[appID.String()] = App{ID: appID.String(), AccountID: actorID.String(), Slug: "cancel-me", Status: AppActive}
	deployment, err := store.CreateDeployment(ctx, Deployment{AppID: appID.String(), Status: DeployBuilding})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	deploymentID := uuid.MustParse(deployment.ID)
	entry := OrgActivity{
		OrgID: orgID, Kind: "deploy.cancelled", ActorType: OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "Bahadir",
		ResourceType: "app", ResourceID: appID.String(), ResourceLabel: "cancel-me",
		AppID: &appID, DeploymentID: &deploymentID,
		SourceType: "deployment.cancelled", SourceID: "cancel:" + deployment.ID,
		Data: []byte(`{"phase":"cancelled","reason":"user","previous_status":"building"}`),
	}
	bad := entry
	bad.Data = []byte(`[]`)
	if _, _, _, err := store.CancelDeploymentTxWithActivity(ctx, deployment.ID, actorID.String(), CancelReasonUser, bad); err == nil {
		t.Fatal("cancellation with invalid activity succeeded")
	}
	if current, err := store.DeploymentByID(ctx, deployment.ID); err != nil || current.Status != DeployBuilding {
		t.Fatalf("deployment after invalid activity = (%+v, %v); want building", current, err)
	}
	if len(store.orgActivityOutbox) != 0 {
		t.Fatalf("outbox after invalid activity = %#v; want empty", store.orgActivityOutbox)
	}

	cancelled, _, outboxID, err := store.CancelDeploymentTxWithActivity(ctx, deployment.ID, actorID.String(), CancelReasonUser, entry)
	if err != nil || cancelled.Status != DeployCancelled || outboxID == 0 {
		t.Fatalf("cancel with activity = (%+v, %d, %v); want cancelled and queued event", cancelled, outboxID, err)
	}
	if delivered, err := store.DeliverOrgActivityOutbox(ctx, outboxID); err != nil || !delivered {
		t.Fatalf("deliver cancellation activity = (%v, %v); want true", delivered, err)
	}
	rows, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].Kind != "deploy.cancelled" || rows[0].ActorLabel != entry.ActorLabel || rows[0].DeploymentID == nil || *rows[0].DeploymentID != deploymentID {
		t.Fatalf("cancellation timeline = (%#v, %v); want one event attributed to the actor/deployment", rows, err)
	}
	var data map[string]any
	if err := json.Unmarshal(rows[0].Data, &data); err != nil || data["phase"] != "cancelled" || data["reason"] != "user" {
		t.Fatalf("cancellation data = %s, %v; want phase and reason", rows[0].Data, err)
	}
}

func TestMemStoreOrgActivityDomainMutationAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	orgID, appUUID, actorID := uuid.New(), uuid.New(), uuid.New()
	appID := appUUID.String()
	store.apps[appID] = App{ID: appID, AccountID: actorID.String(), Slug: "domains", Status: AppActive}
	entry := OrgActivity{
		OrgID: orgID, Kind: "domain.added", ActorType: OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "domain", ResourceID: "payments.example.com",
		ResourceLabel: "payments.example.com", AppID: &appUUID,
		SourceType: "domain.added", SourceID: "create-1", Data: []byte(`{"app_id":"` + appID + `"}`),
	}

	bad := entry
	bad.Data = []byte(`[]`)
	if _, _, err := store.CreateCustomDomainIfUnderQuotaWithActivity(ctx, entry.ResourceID, appID, "token", 10, 10, bad); err == nil {
		t.Fatal("domain create with invalid activity succeeded")
	}
	if _, err := store.DomainByName(ctx, entry.ResourceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("domain after rejected create/activity = %v, want ErrNotFound", err)
	}

	created, createOutboxID, err := store.CreateCustomDomainIfUnderQuotaWithActivity(ctx, entry.ResourceID, appID, "token", 10, 10, entry)
	if err != nil || created.Domain != entry.ResourceID || createOutboxID == 0 {
		t.Fatalf("transactional domain create = (%+v, %d, %v)", created, createOutboxID, err)
	}
	remove := entry
	remove.Kind = "domain.removed"
	remove.SourceType = "domain.removed"
	remove.SourceID = "delete-1"
	badRemove := remove
	badRemove.Data = []byte(`[]`)
	if _, err := store.DeleteCustomDomainWithActivity(ctx, created.Domain, badRemove); err == nil {
		t.Fatal("domain delete with invalid activity succeeded")
	}
	if _, err := store.DomainByName(ctx, created.Domain); err != nil {
		t.Fatalf("domain after rejected delete/activity = %v, want row to remain", err)
	}

	removeOutboxID, err := store.DeleteCustomDomainWithActivity(ctx, created.Domain, remove)
	if err != nil || removeOutboxID == 0 {
		t.Fatalf("transactional domain delete = (%d, %v)", removeOutboxID, err)
	}
	if _, err := store.DeleteCustomDomainWithActivity(ctx, created.Domain, remove); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate domain delete = %v, want ErrNotFound", err)
	}
	if _, err := store.DomainByName(ctx, created.Domain); !errors.Is(err, ErrNotFound) {
		t.Fatalf("domain after delete = %v, want ErrNotFound", err)
	}

	for _, id := range []int64{createOutboxID, removeOutboxID} {
		if delivered, err := store.DeliverOrgActivityOutbox(ctx, id); err != nil || !delivered {
			t.Fatalf("deliver domain activity %d = (%v, %v), want true", id, delivered, err)
		}
	}
	rows, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: orgID, Limit: 10})
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

func TestMemStoreAppLifecycleActivityAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	createdAccount, err := store.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "activity-app@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
	}
	account := createdAccount.Account
	actorID := uuid.MustParse(account.ID)
	createdEntry := OrgActivity{
		Kind: "app.created", ActorType: OrgActivityActorUser, ActorAccountID: &actorID,
		ActorLabel: account.Email, ResourceType: "app", SourceType: "app.created",
		SourceID: "create-app-1", Data: []byte(`{"phase":"created","app_type":"app"}`),
	}
	badCreate := createdEntry
	badCreate.Data = []byte(`[]`)
	if _, _, err := store.CreateAppIfUnderQuotaWithActivity(ctx, App{AccountID: account.ID, Slug: "atomic-create"}, api.MustLimitsFor(api.PlanPro), badCreate); err == nil {
		t.Fatal("create app with invalid activity succeeded")
	}
	if _, err := store.AppBySlug(ctx, "atomic-create"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app after rejected create/activity = %v, want ErrNotFound", err)
	}

	app, createdID, err := store.CreateAppIfUnderQuotaWithActivity(ctx, App{AccountID: account.ID, Slug: "atomic-create"}, api.MustLimitsFor(api.PlanPro), createdEntry)
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
	if _, _, err := store.ScheduleAppDeletionWithActivity(ctx, app.ID, time.Now().UTC().Add(time.Hour), badDelete); err == nil {
		t.Fatal("delete app with invalid activity succeeded")
	}
	if current, err := store.AppBySlug(ctx, app.Slug); err != nil || current.Status == AppDeleted {
		t.Fatalf("app after rejected delete/activity = (%+v, %v), want active", current, err)
	}
	deleted, deletedID, err := store.ScheduleAppDeletionWithActivity(ctx, app.ID, time.Now().UTC().Add(time.Hour), deletedEntry)
	if err != nil || deleted.Status != AppDeleted || deletedID == 0 {
		t.Fatalf("transactional app delete = (%+v, %d, %v), want deleted with event", deleted, deletedID, err)
	}
	duplicateDelete := deletedEntry
	duplicateDelete.SourceID = "delete-app-duplicate"
	if _, duplicateID, err := store.ScheduleAppDeletionWithActivity(ctx, app.ID, time.Now().UTC().Add(2*time.Hour), duplicateDelete); err != nil || duplicateID != 0 {
		t.Fatalf("repeat app delete = (%d, %v), want idempotent result without another event", duplicateID, err)
	}

	restoredEntry := createdEntry
	restoredEntry.Kind = "app.restored"
	restoredEntry.SourceType = "app.restored"
	restoredEntry.SourceID = "restore-app-1"
	restoredEntry.Data = []byte(`{"phase":"restored"}`)
	restored, restoredID, err := store.RestoreAppWithActivity(ctx, app.ID, restoredEntry)
	if err != nil || restored.Status != AppActive || restoredID == 0 {
		t.Fatalf("transactional app restore = (%+v, %d, %v), want active with event", restored, restoredID, err)
	}

	for _, id := range []int64{createdID, deletedID, restoredID} {
		if delivered, err := store.DeliverOrgActivityOutbox(ctx, id); err != nil || !delivered {
			t.Fatalf("deliver app lifecycle event %d = (%v, %v), want true", id, delivered, err)
		}
	}
	rows, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: uuid.MustParse(app.OrgID), Limit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatalf("app lifecycle timeline = (%#v, %v), want three events", rows, err)
	}
	appUUID := uuid.MustParse(app.ID)
	kinds := map[string]bool{}
	for _, row := range rows {
		kinds[row.Kind] = true
		if row.ResourceLabel != app.Slug || row.AppID == nil || *row.AppID != appUUID {
			t.Errorf("app lifecycle identity = %+v, want slug %q and app id %q", row, app.Slug, app.ID)
		}
	}
	if !kinds["app.created"] || !kinds["app.deleted"] || !kinds["app.restored"] {
		t.Fatalf("app lifecycle kinds = %v, want create/delete/restore", kinds)
	}
}

func TestMemStoreAppConfigActivityAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	createdAccount, err := store.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: "activity-config@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
	}
	app, err := store.CreateAppIfUnderQuota(ctx, App{AccountID: createdAccount.Account.ID, Slug: "atomic-config", RAMMB: 256}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	actorID := uuid.MustParse(createdAccount.Account.ID)
	appID := uuid.MustParse(app.ID)
	entry := OrgActivity{
		OrgID: uuid.MustParse(app.OrgID), Kind: "app.config_updated", ActorType: OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: createdAccount.Account.Email,
		ResourceType: "app", ResourceID: app.ID, ResourceLabel: app.Slug,
		AppID: &appID, SourceType: "app.config_updated", SourceID: "config-1",
		Data: []byte(`{"changes":[{"field":"ram_mb","old":256,"new":512}]}`),
	}
	ram := 512
	updated, outboxID, err := store.UpdateAppWithActivity(ctx, app.ID, UpdateAppParams{RAMMB: &ram}, entry,
		func(before, after App) (json.RawMessage, bool, error) {
			if before.RAMMB != 256 || after.RAMMB != 512 {
				t.Fatalf("builder rows = (%d, %d), want exact before/after values", before.RAMMB, after.RAMMB)
			}
			return entry.Data, true, nil
		})
	if err != nil || updated.RAMMB != 512 || outboxID == 0 {
		t.Fatalf("transactional app config update = (%+v, %d, %v)", updated, outboxID, err)
	}

	// A no-op callback can commit the config write without adding a timeline row.
	updated, noOpID, err := store.UpdateAppWithActivity(ctx, app.ID, UpdateAppParams{RAMMB: &ram}, entry,
		func(before, after App) (json.RawMessage, bool, error) {
			if before.RAMMB != after.RAMMB {
				t.Fatalf("no-op builder rows differ: %d != %d", before.RAMMB, after.RAMMB)
			}
			return nil, false, nil
		})
	if err != nil || updated.RAMMB != 512 || noOpID != 0 {
		t.Fatalf("no-op app config update = (%+v, %d, %v), want no event", updated, noOpID, err)
	}

	// A callback failure rolls back both the config write and its event.
	tooMuchRAM := 768
	badEntry := entry
	badEntry.SourceID = "config-2"
	if _, _, err := store.UpdateAppWithActivity(ctx, app.ID, UpdateAppParams{RAMMB: &tooMuchRAM}, badEntry,
		func(_, _ App) (json.RawMessage, bool, error) {
			return nil, false, errors.New("reject event")
		}); err == nil {
		t.Fatal("app config update with a failing activity builder succeeded")
	}
	current, err := store.AppByID(ctx, app.ID)
	if err != nil || current.RAMMB != 512 {
		t.Fatalf("app after rejected config/activity = (%+v, %v), want unchanged config", current, err)
	}

	claimed, err := store.ClaimOrgActivityOutbox(ctx, "app-config-test", time.Minute)
	if err != nil || claimed.ID != outboxID || claimed.Activity.SourceID != entry.SourceID {
		t.Fatalf("claim config activity = (%+v, %v), want only the successful event", claimed, err)
	}
	if delivered, err := store.DeliverOrgActivityOutbox(ctx, outboxID); err != nil || !delivered {
		t.Fatalf("deliver config activity = (%v, %v), want true", delivered, err)
	}
	if _, err := store.ClaimOrgActivityOutbox(ctx, "app-config-test", time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second config activity claim = %v, want no additional event", err)
	}
}
