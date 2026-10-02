//go:build !no_pg

package state

// adr: 429. Real PostgreSQL raw-owner, enrollment and membership fences.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const insertLegacyOwnerApp = `INSERT INTO apps (id,account_id,org_id,project_id,slug,ram_mb,status)
 VALUES ($1,$2,NULL,nullif($3::text,'')::uuid,$4,128,'active')`
const insertLegacyOwnerAssignment = `INSERT INTO application_standard_assignments
 (id,org_id,scope,scope_id,standard_id,admission_version,active,created_by) VALUES ($1,$2,$3,$4,$5,$6,true,$7)`

func postgresLegacyOwnerStore(t *testing.T) (*PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return NewPgStore(pool), pool
}

func postgresLegacyOwnerWrites(t *testing.T, s *PgStore, pool *pgxpool.Pool) legacyOwnerWrites {
	return legacyOwnerWrites{
		create: func(app App) (App, error) {
			if _, err := pool.Exec(t.Context(), insertLegacyOwnerApp, app.ID, app.AccountID, app.ProjectID, app.Slug); err != nil {
				return App{}, mapErr(err)
			}
			return s.AppByID(t.Context(), app.ID)
		},
		attach: func(appID, orgID string) error {
			_, err := pool.Exec(t.Context(), `UPDATE apps SET org_id=nullif($2::text,'')::uuid WHERE id=$1`, appID, orgID)
			return mapErr(err)
		},
		seed: func(a appstandards.Assignment, actor string) error {
			_, err := pool.Exec(t.Context(), insertLegacyOwnerAssignment, a.ID, a.OrgID, a.Scope, a.ScopeID, a.StandardID, a.AdmissionVersion, actor)
			return mapErr(err)
		},
		erase: func(appID string) error {
			_, err := pool.Exec(t.Context(), `DELETE FROM apps WHERE id=$1`, appID)
			return mapErr(err)
		},
	}
}

func TestPgLegacyStandardOwnerProjectReviewAndRestore(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	legacyOwnerProjectReviewAndRestore(t, s, postgresLegacyOwnerWrites(t, s, pool))
}

func TestPgLegacyStandardOwnerAttachment(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	legacyOwnerAttachmentCapturesStandards(t, s, postgresLegacyOwnerWrites(t, s, pool))
}

func TestPgLegacyStandardOwnerUnmanagedRemoval(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	legacyOwnerUnmanagedRemoval(t, s, postgresLegacyOwnerWrites(t, s, pool))
}

func TestPgLegacyStandardOwnerRetainedApplicationScope(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	legacyOwnerRetainedApplicationScope(t, s, postgresLegacyOwnerWrites(t, s, pool))
}

func TestPgLegacyStandardOwnerProjectMembershipRace(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	owner, project, assignment := legacyOwnerFixture(t, s)
	appID := uuid.NewString()
	appArgs := []any{appID, owner.Account.ID, project.ID, "unowned-race-member"}
	assignmentArgs := []any{assignment.ID, assignment.OrgID, assignment.Scope, assignment.ScopeID, assignment.StandardID, assignment.AdmissionVersion, owner.Account.ID}
	member, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer member.Rollback(t.Context())
	if _, err := member.Exec(t.Context(), insertLegacyOwnerApp, appArgs...); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	_, err = pool.Exec(ctx, insertLegacyOwnerAssignment, assignmentArgs...)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("assignment did not wait for uncommitted unowned member: %v", err)
	}
	if err := member.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), insertLegacyOwnerAssignment, assignmentArgs...)
	assertLegacyOwnerScopeFailure(t, err)
	if _, err := pool.Exec(t.Context(), `DELETE FROM apps WHERE id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	assigned, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer assigned.Rollback(t.Context())
	if _, err := assigned.Exec(t.Context(), insertLegacyOwnerAssignment, assignmentArgs...); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 150*time.Millisecond)
	_, err = pool.Exec(ctx, insertLegacyOwnerApp, appArgs...)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unowned member did not wait for uncommitted assignment: %v", err)
	}
	if err := assigned.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), insertLegacyOwnerApp, appArgs...)
	assertLegacyOwnerScopeFailure(t, err)
}

func assertLegacyOwnerScopeFailure(t *testing.T, err error) {
	t.Helper()
	var value *pgconn.PgError
	if !errors.As(err, &value) || value.Code != "23514" || value.ConstraintName != "application_standard_scope_owner" {
		t.Fatalf("nullable owner bypassed assigned scope: %v", err)
	}
}

func TestPgLegacyStandardOwnerRuntimeCompatibility(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	legacyOwnerRuntimeCompatibility(t, s, postgresLegacyOwnerWrites(t, s, pool), func(app App, ins Instance) {
		var input []byte
		err := pool.QueryRow(t.Context(), `SELECT application_standard_native_runtime_snapshot($1,$2)`, app.ID, ins.DeploymentID).Scan(&input)
		var value *pgconn.PgError
		if !errors.As(err, &value) || value.ConstraintName != "application_standards_pending" {
			t.Fatalf("unowned app acquired runtime authority: %v", err)
		}
	})
}

func TestPgLegacyStandardOwnerRuntimeEligibility(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	legacyOwnerRuntimeEligibility(t, s, postgresLegacyOwnerWrites(t, s, pool))
}

func TestPgLegacyStandardOwnerRuntimeAttachmentFence(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	owner, _, assignment := legacyOwnerFixture(t, s)
	assignment.Scope, assignment.ScopeID = "organization", owner.PersonalOrg.ID
	writes := postgresLegacyOwnerWrites(t, s, pool)
	if err := writes.seed(assignment, owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	app, err := writes.create(App{ID: uuid.NewString(), AccountID: owner.Account.ID, Slug: "legacy-runtime-race", RAMMB: 128, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(t.Context(), DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `UPDATE apps SET org_id=$2 WHERE id=$1`, app.ID, owner.PersonalOrg.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	_, err = s.CreateInstance(ctx, app.ID, "", string(StateWaking), 128, node.ID, uuid.NewString())
	cancel()
	if !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
		t.Fatalf("runtime missed uncommitted owner attachment: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInstance(t.Context(), app.ID, "", string(StateWaking), 128, node.ID, uuid.NewString()); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("attached owner admitted an uninstalled runtime: %v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM instances WHERE app_id=$1`, app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("refused runtime left an allocation: %d %v", count, err)
	}
}
