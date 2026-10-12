// adr: 1027 — persist permission-pinned write evidence and enforce its command shape.
package state_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingVerificationStoreMem(t *testing.T) {
	bindingVerificationStoreSuite(t, state.NewMemStore())
}
func TestBindingVerificationStorePG(t *testing.T) {
	store, _ := pgStore(t)
	bindingVerificationStoreSuite(t, store)
}

func TestObjectStorageWriteVerificationConstraintPG(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	bindingVerificationStoreSuite(t, store)
	for _, statement := range []string{
		`UPDATE app_tasks SET command[3]='write' WHERE binding_verification->>'object_storage_mode'='read_write'`,
		`UPDATE app_tasks SET binding_verification=binding_verification - 'object_storage_mode' WHERE binding_verification->>'object_storage_mode'='read_write'`,
		`UPDATE app_tasks SET binding_verification=jsonb_set(binding_verification,'{object_storage_mode}','"read"') WHERE binding_verification->>'object_storage_mode'='read_write'`,
		`UPDATE app_tasks SET binding_verification=jsonb_set(binding_verification,'{type}','"postgres"') WHERE binding_verification->>'object_storage_mode'='read_write'`,
	} {
		_, err := pool.Exec(ctx, statement)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatalf("database accepted invalid mode/command pin: %v", err)
		}
	}
	raw, err := os.ReadFile("../../migrations/20261011120000001_object_storage_write_probe.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing rollback")
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var pins int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM app_tasks WHERE binding_verification->>'object_storage_mode' IN ('write','read_write')`).Scan(&pins); err != nil || pins != 0 {
		t.Fatalf("rollback retained unqualified pins: %d %v", pins, err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatalf("reapply: %v", err)
	}
}

func bindingVerificationStoreSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@verification.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "verification-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, Scope: "default", ImageDigest: "sha256:" + strings.Repeat("1", 64), CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 4096); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tasks := store.(state.AppTaskStore)
	evidence := store.(state.BindingVerificationStore)
	now := time.Now().UTC().Truncate(time.Microsecond)
	params := state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual,
		Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}, CreatedAt: now, MaxOutputBytes: 4096,
		BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypePostgres, Binding: "DATABASE_URL", Revision: strings.Repeat("a", 64)},
	}
	old, err := tasks.CreateAppTask(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	params.BindingVerification.Revision = strings.Repeat("b", 64)
	params.CreatedAt = now.Add(time.Second)
	latest, err := tasks.CreateAppTask(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	params.BindingVerification.Revision = "MUTATED"
	first, err := tasks.ClaimNextAppTask(ctx, "verification-test", now.Add(2*time.Second), time.Minute)
	if err != nil || first.ID != old.ID {
		t.Fatalf("claim=%+v err=%v", first, err)
	}
	first, err = tasks.MarkAppTaskRunning(ctx, first.ID, *first.LeaseToken, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	_, err = tasks.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: first.ID, LeaseToken: *first.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &zero, StdoutTail: `{"private":"RAW_OUTPUT"}`, FinishedAt: now.Add(4 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := evidence.ListBindingVerificationTasks(ctx, account.ID, app.ID, []string{api.BindingTypePostgres})
	if err != nil || len(rows) != 1 || rows[0].Status != "queued" || rows[0].Pin.Revision != strings.Repeat("b", 64) || rows[0].DeploymentID != dep.ID {
		t.Fatalf("newest admission=%+v err=%v latest=%s", rows, err, latest.ID)
	}
	rows[0].Pin.Revision = "MUTATED"
	rows, err = evidence.ListBindingVerificationTasks(ctx, account.ID, app.ID, []string{api.BindingTypePostgres})
	if err != nil || rows[0].Pin.Revision != strings.Repeat("b", 64) {
		t.Fatalf("mutable projection=%+v err=%v", rows, err)
	}
	for _, input := range []struct {
		account, app string
		kinds        []string
	}{
		{uuid.NewString(), app.ID, []string{api.BindingTypePostgres}},
		{account.ID, uuid.NewString(), []string{api.BindingTypePostgres}},
		{account.ID, app.ID, []string{api.BindingTypeService}},
	} {
		rows, err = evidence.ListBindingVerificationTasks(ctx, input.account, input.app, input.kinds)
		if err != nil || len(rows) != 0 {
			t.Fatalf("isolation=%+v err=%v", rows, err)
		}
	}
	params.BindingVerification.Revision = strings.Repeat("a", 64)
	params.Command = []string{"echo", "DATABASE_URL"}
	if _, err := tasks.CreateAppTask(ctx, params); err == nil {
		t.Fatal("ordinary command acquired a verification pin")
	}
	params.Command = []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}
	params.BindingVerification = &state.BindingVerificationPin{Type: api.BindingTypeObjectStorage, Binding: "ASSETS", Revision: strings.Repeat("c", 64)}
	objectTask, err := tasks.CreateAppTask(ctx, params)
	if err != nil {
		t.Fatalf("object-storage pin admission: %v", err)
	}
	rows, err = evidence.ListBindingVerificationTasks(ctx, account.ID, app.ID, []string{api.BindingTypeObjectStorage})
	if err != nil || len(rows) != 1 || rows[0].Pin.Type != api.BindingTypeObjectStorage || objectTask.BindingVerification == nil {
		t.Fatalf("object-storage evidence=%+v task=%+v err=%v", rows, objectTask, err)
	}
	params.Command[0] = api.AppTaskPostgresBindingProbeCommand
	if _, err := tasks.CreateAppTask(ctx, params); err == nil {
		t.Fatal("object-storage pin admitted with PostgreSQL command")
	}
	params.Command = []string{api.AppTaskObjectStorageWriteProbeCommand, "ASSETS", "write"}
	params.BindingVerification.ObjectStorageMode = "write"
	params.CreatedAt = now.Add(4 * time.Second)
	writeTask, err := tasks.CreateAppTask(ctx, params)
	if err != nil {
		t.Fatalf("write probe admission: %v", err)
	}
	rows, err = evidence.ListBindingVerificationTasks(ctx, account.ID, app.ID, []string{api.BindingTypeObjectStorage})
	if err != nil || len(rows) != 1 || rows[0].Pin.ObjectStorageMode != "write" || writeTask.BindingVerification.ObjectStorageMode != "write" {
		t.Fatalf("write permission not persisted: %+v %v", rows, err)
	}
	params.Command[2] = "read_write"
	if _, err = tasks.CreateAppTask(ctx, params); err == nil {
		t.Fatal("mismatched permission pin admitted")
	}
	params.BindingVerification.ObjectStorageMode = "read_write"
	if _, err = tasks.CreateAppTask(ctx, params); err != nil {
		t.Fatalf("read/write mode rejected: %v", err)
	}
	params.Command = []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}
	if _, err = tasks.CreateAppTask(ctx, params); err == nil {
		t.Fatal("legacy command admitted with a write pin")
	}
}
