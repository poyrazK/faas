// adr:1659 — resumable migration evidence over existing runtime owners.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationVerificationHTTPAdmissionAndRecovery(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	app, dep := apidserver.SeedAppTaskDeployment(t, e, "migration-verify")
	in := api.CreateMigrationVerificationRequest{ID: uuid.NewString(), DeploymentID: dep.ID, Checks: api.MigrationDependencyChecks{PostgresEnvKey: "DATABASE_URL"}, SmokePath: "/ready", ColdWake: true}
	base := "/v1/apps/" + app.Slug + "/migration-verifications"
	read := func(method, path string, body any) api.MigrationVerification {
		t.Helper()
		rec := e.Do(t, method, path, body, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
		}
		var out api.MigrationVerification
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	created := read(http.MethodPost, base, in)
	if created.ID != in.ID {
		t.Fatal("request identity replaced")
	}
	first := read(http.MethodPost, base+"/"+in.ID+"/advance", nil)
	if first.DependencyTaskID == "" {
		t.Fatal("no dependency task")
	}
	resumed := read(http.MethodPost, base, in)
	if resumed.DependencyTaskID != first.DependencyTaskID {
		t.Fatal("recreated dependency task")
	}
	replayed := read(http.MethodPost, base+"/"+in.ID+"/advance", nil)
	if replayed.DependencyTaskID != first.DependencyTaskID {
		t.Fatal("advance recreated dependency task")
	}
	if err := (*e.Store()).MarkAppRuntimeConfigChanged(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	read(http.MethodPost, base+"/"+in.ID+"/advance", nil)
	blocked := read(http.MethodGet, base+"/"+in.ID, nil)
	if blocked.Status != "blocked" {
		t.Fatalf("stale report remained valid: %+v", blocked)
	}
	bad := in
	bad.ID = uuid.NewString()
	bad.SmokePath = "https://other.test/"
	rec := e.Do(t, http.MethodPost, base, bad, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid route: %d %s", rec.Code, rec.Body)
	}
}

func TestMigrationVerificationHTTPWorkerAndCancellationWithTaskAdmissionDisabled(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	app, dep := apidserver.SeedAppTaskDeployment(t, e, "migration-worker")
	in := api.CreateMigrationVerificationRequest{ID: uuid.NewString(), DeploymentID: dep.ID, Checks: api.MigrationDependencyChecks{PostgresEnvKey: "DATABASE_URL"}, SmokePath: "/ready", ColdWake: true}
	base := "/v1/apps/" + app.Slug + "/migration-verifications"
	created := e.Do(t, http.MethodPost, base, in, nil)
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	if err := (*e.S()).DrainMigrationVerificationsForTest(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := (*e.Store()).ListAppTasks(t.Context(), (*e.Acct()).ID, app.ID, 20, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("worker task admission: %d %v", len(rows), err)
	}
	(*e.S()).WithAppTaskAPIEnabled(false)
	for range 2 {
		cancelled := e.Do(t, http.MethodPost, base+"/"+in.ID+"/cancel", nil, nil)
		if cancelled.Code != http.StatusOK {
			t.Fatalf("cancel unavailable with admission off: %d %s", cancelled.Code, cancelled.Body)
		}
	}
	if err := (*e.S()).DrainMigrationVerificationsForTest(t.Context()); err != nil {
		t.Fatal(err)
	}
	result := e.Do(t, http.MethodGet, base+"/"+in.ID, nil, nil)
	var report api.MigrationVerification
	if err := json.Unmarshal(result.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if result.Code != http.StatusOK || report.Status != "cancelled" {
		t.Fatalf("worker did not finish cancellation: %d %s", result.Code, result.Body)
	}
	task, err := (*e.Store()).AppTaskByID(t.Context(), (*e.Acct()).ID, app.ID, rows[0].ID)
	if err != nil || task.Status != state.AppTaskCancelled {
		t.Fatalf("queued task not stopped: %+v %v", task, err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "read only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	denied := e.DoAs(t, http.MethodPost, base+"/"+in.ID+"/cancel", nil, nil, key)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read-only cancellation accepted: %d %s", denied.Code, denied.Body)
	}
}

type migrationRecoveryNotifier struct{ calls []db.Notification }

func (n *migrationRecoveryNotifier) Notify(_ context.Context, channel, payload string) error {
	n.calls = append(n.calls, db.Notification{Channel: channel, Payload: payload})
	return nil
}
func (*migrationRecoveryNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, func(), error) {
	return nil, func() {}, nil
}
func (*migrationRecoveryNotifier) WaitFor(context.Context, string, func(string) bool, time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

func TestMigrationVerificationServiceRecoveryRequiresRunningAndPreservesNewDeployment(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, dep := apidserver.SeedAppTaskDeployment(t, e, "migration-restore")
	m := *e.Store()
	n := &migrationRecoveryNotifier{}
	*(*e.S()).Notif() = n
	run := api.MigrationVerification{DeploymentID: dep.ID, WakeID: uuid.NewString()}
	old, err := m.CreateInstance(t.Context(), app.ID, dep.ID, "running", 128, "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := m.BeginAppParkTransition(t.Context(), app.ID, state.AppActive); err != nil || !changed {
		t.Fatalf("park fixture did not commit: %t %v", changed, err)
	}
	restore := func() string {
		t.Helper()
		status, err := (*e.S()).RestoreMigrationServiceForTest(t.Context(), (*e.Acct()).ID, app.ID, run)
		if err != nil {
			t.Fatal(err)
		}
		return status
	}
	if status := restore(); status != "pending" || len(n.calls) != 1 || n.calls[0].Channel != db.NotifyAppChanged {
		t.Fatalf("woke before drain: %s %+v", status, n.calls)
	}
	if err := m.UpdateInstanceState(t.Context(), old.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if status := restore(); status != "pending" {
			t.Fatalf("notification treated as recovery: %s", status)
		}
		last := n.calls[len(n.calls)-1]
		var payload map[string]string
		if err := json.Unmarshal([]byte(last.Payload), &payload); err != nil {
			t.Fatal(err)
		}
		if last.Channel != db.NotifyAppWake || payload["wake_id"] != run.WakeID {
			t.Fatalf("replaced wake identity: %+v", payload)
		}
	}
	if _, err := m.CompareAndSetAppStatus(t.Context(), app.ID, state.AppEvictedCold, state.AppActive); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateInstance(t.Context(), app.ID, dep.ID, "running", 128, "", run.WakeID); err != nil {
		t.Fatal(err)
	}
	if status := restore(); status != "restored" {
		t.Fatalf("running deployment not restored: %s", status)
	}
	count := len(n.calls)
	next, err := m.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, ImageDigest: "sha256:new", CreatedAt: time.Now().Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(t.Context(), next.ID); err != nil {
		t.Fatal(err)
	}
	if status := restore(); status != "superseded" || len(n.calls) != count {
		t.Fatalf("recovery touched replaced deployment: %s %+v", status, n.calls)
	}
}

func TestMigrationVerificationRecoveryRespectsInactiveAccount(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	app, dep := apidserver.SeedAppTaskDeployment(t, e, "migration-held")
	n := &migrationRecoveryNotifier{}
	*(*e.S()).Notif() = n
	if _, changed, err := (*e.Store()).BeginAppParkTransition(t.Context(), app.ID, state.AppActive); err != nil || !changed {
		t.Fatalf("park fixture did not commit: %t %v", changed, err)
	}
	changed, err := (*e.Store()).SuspendAccountForFreeQuota(t.Context(), (*e.Acct()).ID)
	if err != nil || !changed {
		t.Fatalf("account suspension: %t %v", changed, err)
	}
	status, err := (*e.S()).RestoreMigrationServiceForTest(t.Context(), (*e.Acct()).ID, app.ID, api.MigrationVerification{DeploymentID: dep.ID, WakeID: uuid.NewString()})
	if err != nil || status != "blocked" || len(n.calls) != 0 {
		t.Fatalf("woke inactive account: %s %v %+v", status, err, n.calls)
	}
}
