//go:build !no_pg

package migrations_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationCatalogRuntimeScopesUpgradeAndPopulatedReplay(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261003214107898)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "catalog-runtime-scope@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "catalog-scopes"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "catalog-worker",
		Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	legacyScope := strings.Repeat("a", 40)
	if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, legacyScope, "MODE", "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(ctx, account.ID, app.ID, legacyScope, "DATABASE", []byte("original-sealed")); err != nil {
		t.Fatal(err)
	}
	// Seed through the historical schema: the current deployment writer also
	// reads workload specification tables introduced after this upgrade point.
	legacy := state.Deployment{AppID: app.ID, Scope: legacyScope, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("a", 64), Status: state.DeployLive}
	if err := pool.QueryRow(ctx, `INSERT INTO deployments(id,app_id,scope,kind,image_digest,status)
		VALUES(gen_random_uuid(),$1,$2,$3,$4,$5) RETURNING id`,
		app.ID, legacy.Scope, legacy.Kind, legacy.ImageDigest, legacy.Status).Scan(&legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "1", "MODE", "short"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(ctx, account.ID, app.ID, "1", "DATABASE", []byte("short-sealed")); err != nil {
		t.Fatal(err)
	}
	if err := store.PutAppEnvironmentSecretReference(ctx, account.ID, app.ID, "1", "DATABASE_URL", "secret:DATABASE"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppEnvironmentSecretReference(ctx, account.ID, app.ID, "1", "REMOVED"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceProjectDeployBranches(ctx, account.ID, project.ID, map[string]string{"main": "1"}); err != nil {
		t.Fatal(err)
	}
	queue, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID,
		DeploymentScope: "1", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true,
		WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue,
		QueueName: "orders", QueueBindingID: queue.Binding.ID, DeploymentScope: "1", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.InsertTriggerRecord(ctx, queue.Changes[0].TriggerID, accepted.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=20261004013552452`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	old, err := store.DeploymentByID(ctx, legacy.ID)
	if err != nil || old.Scope != legacyScope || old.Status != legacy.Status {
		t.Fatalf("upgrade changed legacy deployment: %+v %v", old, err)
	}
	secret, err := store.GetAppSecretInScope(ctx, account.ID, app.ID, legacyScope, "DATABASE")
	if err != nil || !bytes.Equal(secret.Ciphertext, []byte("original-sealed")) {
		t.Fatalf("upgrade changed legacy ciphertext: %v", err)
	}
	current, err := store.QueueBindingByID(ctx, account.ID, app.ID, queue.Binding.ID)
	if err != nil || current.DeploymentScope != "1" || current.EnvironmentID != environment.ID {
		t.Fatalf("replay changed queue scope identity: %+v %v", current, err)
	}
	work, err := store.InvocationByID(ctx, accepted.ID)
	if err != nil || work.DeploymentScope != "1" || work.QueueBindingID != queue.Binding.ID {
		t.Fatalf("replay changed accepted work: %+v %v", work, err)
	}
	if id, err := store.TriggerRecordIDByItemIdentifier(ctx, queue.Changes[0].TriggerID, accepted.ID); err != nil || id != receipt {
		t.Fatalf("replay changed receipt: %q %v", id, err)
	}
	intent, err := store.AppEnvironmentSecretIntent(ctx, account.ID, app.ID, "1")
	if err != nil || intent.References["DATABASE_URL"] != "secret:DATABASE" || len(intent.SuppressedKeys) != 1 || intent.SuppressedKeys[0] != "REMOVED" {
		t.Fatalf("replay changed positive or negative intent: %+v %v", intent, err)
	}
	for _, invalid := range []string{"__all__", "", "-a", "a-", "A", "a_b", strings.Repeat("a", 41)} {
		_, err := pool.Exec(ctx, `update app_envs set scope=$1 where app_id=$2 and scope='1'`, invalid, app.ID)
		var problem *pgconn.PgError
		if !errors.As(err, &problem) || problem.ConstraintName != "app_envs_scope_shape" {
			t.Fatalf("invalid scope %q admitted: %v", invalid, err)
		}
	}
	// Execute the actual Down body in its own transaction. A historical
	// grammar cannot represent accepted numeric catalog rows; failure must
	// preserve both those rows and the current constraints.
	body, err := migrations.FS.ReadFile("20261004013552452_catalog_environment_runtime_scopes.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, found := strings.Cut(string(body), "-- +goose Down")
	if !found {
		t.Fatal("migration has no Down body")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, downErr := tx.Exec(ctx, down)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var problem *pgconn.PgError
	if !errors.As(downErr, &problem) || problem.Code != "23514" {
		t.Fatalf("downgrade admitted incompatible existing data: %v", downErr)
	}
	if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "1", "MODE", "still-short"); err != nil {
		t.Fatalf("failed downgrade changed runtime scope constraint: %v", err)
	}
	intent, err = store.AppEnvironmentSecretIntent(ctx, account.ID, app.ID, "1")
	if err != nil || intent.References["DATABASE_URL"] != "secret:DATABASE" || len(intent.SuppressedKeys) != 1 {
		t.Fatalf("failed downgrade changed accepted intent: %+v %v", intent, err)
	}
}
