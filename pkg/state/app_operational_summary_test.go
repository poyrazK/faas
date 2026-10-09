package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppPendingRollbacksMem(t *testing.T) {
	appPendingRollbacksSuite(t, state.NewMemStore())
}

func TestAppPendingRollbacksPG(t *testing.T) {
	store, _ := pgStore(t)
	appPendingRollbacksSuite(t, store)
}

func appPendingRollbacksSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	a, app, target, serving := checkedRollbackFixture(t, store, false)
	checked := store.(state.CheckedRollbackStore)
	operation, err := checked.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, serving.ID, "customer reason")
	if err != nil {
		t.Fatal(err)
	}
	other, otherApp, otherTarget, otherServing := checkedRollbackFixture(t, store, false)
	if _, err = checked.CreateCheckedRollback(ctx, other.ID, otherApp.ID, otherTarget.ID, otherServing.ID, "another app"); err != nil {
		t.Fatal(err)
	}
	reader := store.(state.AppOperationalStore)
	rows, err := reader.ListAppPendingRollbacks(ctx, a.ID, app.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != operation.ID {
		t.Fatalf("app recovery mixed with other account: %+v %v", rows, err)
	}
	after, err := checked.GetCheckedRollback(ctx, a.ID, app.ID, operation.ID)
	if err != nil || !reflect.DeepEqual(operation, after) {
		t.Fatalf("summary read changed rollback: %+v %v", after, err)
	}
	if _, err = reader.ListAppPendingRollbacks(ctx, other.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account rollback read: %v", err)
	}
	if err = checked.UpdateCheckedRollback(ctx, operation, "failed", "rollback_failed", nil); err != nil {
		t.Fatal(err)
	}
	rows, err = reader.ListAppPendingRollbacks(ctx, a.ID, app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("terminal rollback still pending: %+v %v", rows, err)
	}
}

func TestAppPendingRestartsPGUsesLatestHandoffBeforeFiltering(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	a, app, _, _ := checkedRollbackFixture(t, store, false)
	other, otherApp, _, _ := checkedRollbackFixture(t, store, false)
	seed := func(appID, wakeID, status string) {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"app_id": appID, "wake_id": wakeID})
		if err != nil {
			t.Fatal(err)
		}
		productionMonitorExec(t, pool, "INSERT INTO notification_outbox(channel,payload,state,attempts,last_error) VALUES('runtime_config_restart',$1,$2,3,$3)", string(payload), status, "internal PRIVATE_DIAGNOSTIC reason=requests_active")
	}
	completedWake := uuid.NewString()
	seed(app.ID, completedWake, "pending")
	seed(app.ID, completedWake, "delivered")
	for i := 0; i <= api.AppOperationalRecoveryLimit; i++ {
		seed(app.ID, uuid.NewString(), "pending")
	}
	failedWake := uuid.NewString()
	seed(app.ID, failedWake, "dead_letter")
	// Other apps cannot displace this app's rows before the display limit.
	for i := 0; i <= api.AppOperationalRecoveryLimit; i++ {
		seed(otherApp.ID, uuid.NewString(), "dead_letter")
	}
	rows, err := store.ListAppPendingRestarts(ctx, a.ID, app.ID)
	if err != nil || len(rows) != api.AppOperationalRecoveryLimit+1 {
		t.Fatalf("pending restart bounds: %+v %v", rows, err)
	}
	failedFound := false
	for _, row := range rows {
		wantStatus := "retrying"
		if row.WakeID == failedWake {
			wantStatus = "failed"
			failedFound = true
		}
		if row.WakeID == completedWake || row.Status != wantStatus || row.FailureReason != "requests_active" {
			t.Fatalf("old/completed handoff or raw failure leaked: %+v", row)
		}
	}
	if !failedFound {
		t.Fatal("failed restart omitted from recovery summary")
	}
	body, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(body), "PRIVATE_DIAGNOSTIC") {
		t.Fatalf("restart error sanitation: %s %v", body, err)
	}
	if _, err = store.ListAppPendingRestarts(ctx, other.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account restart read: %v", err)
	}
}

func TestAppOpenMonitorIncidentPGReadDoesNotRecoverIncident(t *testing.T) {
	pool, store, account, app, _, deployment := productionMonitorPG(t)
	productionMonitorTraffic(t, pool, account, app, deployment.ID, "POST", "/checkout", 500, 20, 500, false)
	if changed, err := store.EvaluateRouteMonitor(t.Context(), account.ID, app.ID); err != nil || !changed {
		t.Fatalf("open incident: %v %v", changed, err)
	}
	incident, err := store.GetAppOpenMonitorIncident(t.Context(), account.ID, app.ID)
	if err != nil || incident == nil || incident.DeploymentID != deployment.ID {
		t.Fatalf("missing saved incident metadata: %+v %v", incident, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.GetRouteMonitorReport(t.Context(), account.ID, app.ID); err != nil {
			t.Fatal(err)
		}
		current, err := store.GetAppOpenMonitorIncident(t.Context(), account.ID, app.ID)
		if err != nil || !reflect.DeepEqual(incident, current) {
			t.Fatalf("read changed saved recovery state: %+v %v", current, err)
		}
	}
	if _, err = store.GetAppOpenMonitorIncident(t.Context(), uuid.NewString(), app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account incident read: %v", err)
	}
}
