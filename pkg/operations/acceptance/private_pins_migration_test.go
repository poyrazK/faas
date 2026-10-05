// adr: 521
package acceptance_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgOperationPrivatePinsBackfillPreservesPublicDeadlines(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, "private-pin-backfill@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "backfill"})
	if err != nil {
		t.Fatal(err)
	}
	origin, originDep := createOperationGraphApp(t, ctx, s, acct.ID, project.ID, "origin")
	target, targetDep := createOperationGraphApp(t, ctx, s, acct.ID, project.ID, "target")
	release, err := s.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{
		{AppID: origin.ID, DeploymentID: originDep.ID}, {AppID: target.ID, DeploymentID: targetDep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: origin.ID, Scope: "production", DeploymentID: originDep.ID, ReleaseID: release.ID, Spec: operationSpec()}})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "backfill", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	publicDeadline := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Hour)
	if _, err := pool.Exec(ctx, `INSERT INTO deployment_revision_pins(deployment_id,app_id,expires_at) VALUES($1,$2,$3)`,
		originDep.ID, origin.ID, publicDeadline); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE customer_operation_code_pins`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.FS.ReadFile("20261004123650815_operation_private_code_pins.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(migration), "-- +goose Down")[0]
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("backfill/replay %d: %v", attempt, err)
		}
	}
	var count int
	var earliest time.Time
	if err := pool.QueryRow(ctx, `SELECT count(*),min(expires_at) FROM customer_operation_code_pins`).Scan(&count, &earliest); err != nil {
		t.Fatal(err)
	}
	if count != 2 || earliest.Before(op.ExpiresAt.Add(-time.Millisecond)) {
		t.Fatalf("backfill lost source/member retention: %d %s", count, earliest)
	}
	var after time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM deployment_revision_pins WHERE deployment_id=$1`, originDep.ID).Scan(&after); err != nil || !after.Equal(publicDeadline) {
		t.Fatalf("backfill changed public grant: %s %v", after, err)
	}
	_, err = pool.Exec(ctx, `UPDATE customer_operation_code_pins SET app_id=$2 WHERE deployment_id=$1`, targetDep.ID, origin.ID)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23503" {
		t.Fatalf("private receipt accepted a foreign app/deployment pair: %v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE customer_operation_code_pins SET expires_at='infinity' WHERE deployment_id=$1`, originDep.ID)
	if !errors.As(err, &pgError) || pgError.Code != "23514" {
		t.Fatalf("private receipt accepted an unlimited timestamp: %v", err)
	}
}
