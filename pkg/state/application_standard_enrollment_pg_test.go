//go:build !no_pg

package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPgApplicationStandardEnrollment(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	standardEnrollmentLifecycle(t, NewPgStore(pool), func(a appstandards.Assignment, actor string) {
		_, err := pool.Exec(ctx, `INSERT INTO application_standard_assignments
            (id, org_id, scope, scope_id, standard_id, admission_version, active, created_by)
            VALUES ($1, $2, $3, $4, $5, $6, true, $7)
            ON CONFLICT (id) DO UPDATE SET admission_version = EXCLUDED.admission_version,
              revision = application_standard_assignments.revision + 1, updated_at = now()`,
			a.ID, a.OrgID, a.Scope, a.ScopeID, a.StandardID, a.AdmissionVersion, actor)
		if err != nil {
			t.Fatal(err)
		}
	})
	for _, check := range []struct{ query, constraint string }{
		{`UPDATE application_standard_assignments SET admission_version = 1`, "application_standard_assignment_revision"},
		{`UPDATE application_standard_assignments SET scope = 'application', revision = revision + 1`, "application_standard_assignment_revision"},
		{`DELETE FROM application_standard_assignments`, "application_standard_assignment_retention"},
		{`INSERT INTO deployments (id, app_id, kind, status) SELECT gen_random_uuid(), app_id, 'image', 'pending' FROM app_application_standards WHERE state = 'pending' LIMIT 1`, "application_standards_pending"},
	} {
		_, err := pool.Exec(ctx, check.query)
		var failure *pgconn.PgError
		if !errors.As(err, &failure) || failure.Code != "23514" || failure.ConstraintName != check.constraint {
			t.Fatalf("database enrollment fence: %s: %v", check.query, err)
		}
	}
}

func TestPgApplicationStandardProjectScopeFence(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := NewPgStore(pool)
	owner, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "scope-owner@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "scope-foreign@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, Project{AccountID: owner.Account.ID, Slug: "scope-project"})
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "project-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	insertAssignment := `INSERT INTO application_standard_assignments (id, org_id, scope, scope_id, standard_id, admission_version, active, created_by) VALUES ($1, $2, 'project', $3, $4, 1, true, $5)`
	insertApp := `INSERT INTO apps (id, account_id, org_id, project_id, slug, ram_mb) VALUES ($1, $2, $3, $4, 'foreign-member', 128)`
	appID, assignmentID := uuid.NewString(), uuid.NewString()
	appArgs := []any{appID, foreign.Account.ID, foreign.PersonalOrg.ID, project.ID}
	assignmentArgs := []any{assignmentID, owner.PersonalOrg.ID, project.ID, version.StandardID, owner.Account.ID}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, insertApp, appArgs...); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = pool.Exec(deadline, insertAssignment, assignmentArgs...)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("assignment did not wait for uncommitted member: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, insertAssignment, assignmentArgs...)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.ConstraintName != "application_standard_scope_owner" {
		t.Fatalf("foreign project membership missed: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, appID); err != nil {
		t.Fatal(err)
	}
	assigned, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = assigned.Rollback(ctx) }()
	if _, err := assigned.Exec(ctx, insertAssignment, assignmentArgs...); err != nil {
		t.Fatal(err)
	}
	deadline, cancel = context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = pool.Exec(deadline, insertApp, appArgs...)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("member did not wait for uncommitted assignment: %v", err)
	}
	if err := assigned.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, insertApp, appArgs...)
	if !errors.As(err, &failure) || failure.ConstraintName != "application_standard_scope_owner" {
		t.Fatalf("foreign member bypassed assigned project: %v", err)
	}
}
