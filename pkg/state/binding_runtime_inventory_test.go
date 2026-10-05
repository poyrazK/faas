package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingRuntimeInventoryMem(t *testing.T) {
	store := state.NewMemStore()
	bindingRuntimeInventorySuite(t, store, state.DefaultLocalNodeName, store.BackdateForTest)
}

func TestBindingRuntimeInventoryPG(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	bindingRuntimeInventorySuite(t, store, resolveDefaultLocal(t, ctx, store), func(id string, at time.Time) {
		if _, err := pool.Exec(ctx, `UPDATE instances SET started_at=$2 WHERE id=$1`, id, at); err != nil {
			t.Fatal(err)
		}
	})
}

func bindingRuntimeInventorySuite(t *testing.T, store state.Store, nodeID string, setStartedAt func(string, time.Time)) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@runtime.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "runtime-" + uuid.NewString()[:8], RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	reader := store.(state.BindingRuntimeInventoryStore)
	empty, err := reader.ReadBindingRuntimeInventory(ctx, account.ID, app.ID, "")
	if err != nil || empty.ChangedAt != nil || len(empty.Deployments) != 0 {
		t.Fatalf("empty runtime=%+v err=%v", empty, err)
	}
	deploy := func(scope string, status state.DeploymentStatus) state.Deployment {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, Status: status, ImageDigest: "sha256:runtime"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, dep.ID, status, ""); err != nil {
			t.Fatal(err)
		}
		return dep
	}
	prod, stage, old := deploy("production", state.DeployLive), deploy("staging", state.DeployLive), deploy("production", state.DeploySuperseded)
	unused := deploy("default", state.DeployFailed)
	create := func(dep state.Deployment, instanceState, mode string) state.Instance {
		t.Helper()
		instance, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, instanceState, 512, nodeID, uuid.NewString(), mode)
		if err != nil {
			t.Fatal(err)
		}
		return instance
	}
	stale := create(prod, "running", "normal")
	unknown, err := reader.ReadBindingRuntimeInventory(ctx, account.ID, app.ID, "production")
	if err != nil || len(unknown.Deployments) != 1 || unknown.Deployments[0].Serving.Unknown != 1 {
		t.Fatalf("missing stamp=%+v err=%v", unknown, err)
	}
	create(prod, "warm", "normal")
	create(old, "draining", "normal")
	create(prod, "running", "mirror")
	create(unused, "parked", "normal")
	create(stage, "stopped", "normal")
	if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	stamp, ok, err := store.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil || !ok {
		t.Fatalf("stamp=%v err=%v", stamp, err)
	}
	// Equal timestamps conservatively remain stale, including coarse clocks.
	setStartedAt(stale.ID, stamp)
	create(prod, "running", "normal")
	create(prod, "cold_booting", "normal")
	zero := create(stage, "running", "normal")
	setStartedAt(zero.ID, time.Time{})
	all, err := reader.ReadBindingRuntimeInventory(ctx, account.ID, app.ID, "")
	if err != nil || all.ChangedAt == nil || !all.ChangedAt.Equal(stamp) || len(all.Deployments) != 3 {
		t.Fatalf("runtime=%+v err=%v", all, err)
	}
	byID := map[string]state.BindingRuntimeDeployment{}
	for _, row := range all.Deployments {
		byID[row.ID] = row
	}
	if row := byID[prod.ID]; row.Serving != (state.BindingRuntimeCounts{Current: 1, Stale: 1}) || row.Resident != (state.BindingRuntimeCounts{Current: 2, Stale: 2}) || row.Starting != 1 {
		t.Fatalf("production counts=%+v", row)
	}
	if row := byID[stage.ID]; row.Serving.Unknown != 1 || row.Resident.Unknown != 1 || row.Serving.Current != 0 {
		t.Fatalf("unknown timestamp counts=%+v", row)
	}
	if row := byID[old.ID]; row.Resident.Stale != 1 || row.Serving.Stale != 0 || row.DeploymentStatus != "superseded" {
		t.Fatalf("old deployment resident=%+v", row)
	}
	filtered, err := reader.ReadBindingRuntimeInventory(ctx, account.ID, app.ID, "staging")
	if err != nil || len(filtered.Deployments) != 1 || !reflect.DeepEqual(filtered.Deployments[0], byID[stage.ID]) {
		t.Fatalf("scope filter=%+v err=%v", filtered, err)
	}
	filtered, err = reader.ReadBindingRuntimeInventory(ctx, account.ID, app.ID, "missing")
	if err != nil || len(filtered.Deployments) != 0 || filtered.ChangedAt == nil {
		t.Fatalf("empty scope=%+v err=%v", filtered, err)
	}
	for _, foreign := range []struct{ account, app string }{{uuid.NewString(), app.ID}, {account.ID, uuid.NewString()}} {
		if _, err := reader.ReadBindingRuntimeInventory(ctx, foreign.account, foreign.app, ""); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("tenant isolation: %v", err)
		}
	}
	// Returned data cannot mutate the store's configuration stamp.
	*all.ChangedAt = time.Time{}
	fresh, err := reader.ReadBindingRuntimeInventory(ctx, account.ID, app.ID, "")
	if err != nil || fresh.ChangedAt == nil || !fresh.ChangedAt.Equal(stamp) {
		t.Fatalf("mutable stamp=%+v err=%v", fresh, err)
	}
}

func TestBindingRefreshInventoryPGStatusesAndIsolation(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	wakeID := uuid.NewString()
	payload, _ := json.Marshal(map[string]string{"app_id": appID, "wake_id": wakeID})
	requested := time.Now().UTC().Truncate(time.Microsecond)
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO notification_outbox(channel,payload,created_at) VALUES($1,$2,$3) RETURNING id`, db.NotifyRuntimeConfigRestart, string(payload), requested).Scan(&id); err != nil {
		t.Fatal(err)
	}
	reader := state.BindingRuntimeInventoryStore(store)
	for _, tc := range []struct {
		state, detail, status, reason string
		attempts                      int
	}{
		{"pending", "", "queued", "", 0},
		{"pending", "PRIVATE_PASSWORD reason=telemetry_missing", "retrying", "telemetry_missing", 2},
		{"processing", "reason=requests_active PRIVATE_ORIGIN", "running", "requests_active", 3},
		{"dead_letter", "reason=quiet_period_not_elapsed PRIVATE_ERROR", "failed", "quiet_period_not_elapsed", 4},
		{"dead_letter", "PRIVATE_PASSWORD", "failed", "restart_attempt_failed", 5},
		{"delivered", "", "completed", "", 6},
	} {
		var completed *time.Time
		if tc.state == "delivered" {
			at := requested.Add(time.Minute)
			completed = &at
		}
		if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET state=$2,attempts=$3,last_error=NULLIF($4,''),delivered_at=$5 WHERE id=$1`, id, tc.state, tc.attempts, tc.detail, completed); err != nil {
			t.Fatal(err)
		}
		rows, err := reader.ListBindingRefreshInventory(ctx, accountID, appID, []string{wakeID})
		if err != nil || len(rows) != 1 || rows[0].Status != tc.status || rows[0].FailureReason != tc.reason || rows[0].Attempts != tc.attempts || !rows[0].RequestedAt.Equal(requested) || (rows[0].CompletedAt == nil) != (completed == nil) {
			t.Fatalf("outbox %s rows=%+v err=%v", tc.state, rows, err)
		}
		if completed != nil && !rows[0].CompletedAt.Equal(*completed) {
			t.Fatalf("completion time=%v want=%v", rows[0].CompletedAt, completed)
		}
	}
	for _, input := range []struct {
		account, app string
		ids          []string
	}{
		{uuid.NewString(), appID, []string{wakeID}}, {accountID, uuid.NewString(), []string{wakeID}},
		{accountID, appID, []string{uuid.NewString()}}, {accountID, appID, nil},
	} {
		rows, err := reader.ListBindingRefreshInventory(ctx, input.account, input.app, input.ids)
		if err != nil || len(rows) != 0 {
			t.Fatalf("outbox isolation=%+v err=%v", rows, err)
		}
	}
}
