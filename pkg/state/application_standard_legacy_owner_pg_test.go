//go:build !no_pg

package state

// adr: 435. Real PostgreSQL raw-owner, enrollment and membership fences.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
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

// adr: 435, 595 — #4882 binds every inserted app to an owner: a raw insert
// without an organization takes its project's organization or the account's
// personal one. The unowned legacy state the MemStore cases still model can no
// longer be created in PostgreSQL, so neither can a standards bypass through
// it: the raw insert is enrolled in its assigned scope like any created app.
func TestPgLegacyStandardOwnerRawInsertionIsOwned(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	writes := postgresLegacyOwnerWrites(t, s, pool)
	owner, project, assignment := legacyOwnerFixture(t, s)
	if err := writes.seed(assignment, owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	member, err := writes.create(App{ID: uuid.NewString(), AccountID: owner.Account.ID, ProjectID: project.ID, Slug: "raw-project-member", RAMMB: 128, Status: AppActive})
	if err != nil || !sameStandardUUID(member.OrgID, owner.PersonalOrg.ID) {
		t.Fatalf("raw project member left unowned: %+v %v", member, err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), owner.PersonalOrg.ID, member.ID)
	if err != nil || e.State != "pending" || e.DesiredRevision != 1 || len(e.Adoptions) != 1 || e.Adoptions[0].AssignmentID != assignment.ID {
		t.Fatalf("raw project member escaped its assigned project: %+v %v", e, err)
	}
	if _, err := s.CreateDeployment(t.Context(), Deployment{AppID: member.ID, Kind: DeploymentKindImage, Status: DeployPending}); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("raw project member deployed before installation: %v", err)
	}
	plain, err := writes.create(App{ID: uuid.NewString(), AccountID: owner.Account.ID, Slug: "raw-personal", RAMMB: 128, Status: AppActive})
	if err != nil || !sameStandardUUID(plain.OrgID, owner.PersonalOrg.ID) {
		t.Fatalf("raw app outside a project left unowned: %+v %v", plain, err)
	}
	if err := writes.attach(plain.ID, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("raw app cleared its owner: %v", err)
	}
	foreign, err := s.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: "legacy-foreign@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `INSERT INTO apps (id,account_id,org_id,slug,ram_mb,status) VALUES ($1,$2,$3,'raw-foreign-personal',128,'active')`, uuid.NewString(), owner.Account.ID, foreign.PersonalOrg.ID)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Code != "23514" {
		t.Fatalf("raw app joined another account's personal organization: %v", err)
	}
}

func TestPgLegacyStandardOwnerUnmanagedRemoval(t *testing.T) {
	s, pool := postgresLegacyOwnerStore(t)
	legacyOwnerUnmanagedRemoval(t, s, postgresLegacyOwnerWrites(t, s, pool))
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
