//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgApplicationStandardEnrollment(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	state.StandardEnrollmentLifecycle(t, state.NewPgStore(pool), func(a appstandards.Assignment, actor string) {
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
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	owner, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "scope-owner@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "scope-foreign@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, Slug: "scope-project"})
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "project-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	insertAssignment := `INSERT INTO application_standard_assignments (id, org_id, scope, scope_id, standard_id, admission_version, active, created_by) VALUES ($1, $2, 'project', $3, $4, 1, true, $5)`
	insertApp := `INSERT INTO apps (id, account_id, org_id, project_id, slug, ram_mb) VALUES ($1, $2, $3, $4, 'foreign-member', 128)`
	appID, assignmentID := uuid.NewString(), uuid.NewString()
	appArgs := []any{appID, foreign.Account.ID, foreign.PersonalOrg.ID, project.ID}
	assignmentArgs := []any{assignmentID, owner.PersonalOrg.ID, project.ID, version.StandardID, owner.Account.ID}
	// A project's workloads share its account and organization (#4882), so a
	// foreign account cannot join the project at all, assigned or not; the
	// assignment scope fence is left with no foreign member to refuse.
	foreignMemberRefused := func(label string) {
		t.Helper()
		_, err := pool.Exec(ctx, insertApp, appArgs...)
		var failure *pgconn.PgError
		if !errors.As(err, &failure) || failure.Code != "23514" || failure.ConstraintName != "project_workload_owner" {
			t.Fatalf("%s: foreign project member admitted: %v", label, err)
		}
	}
	foreignMemberRefused("unassigned project")
	if _, err := pool.Exec(ctx, insertAssignment, assignmentArgs...); err != nil {
		t.Fatal(err)
	}
	foreignMemberRefused("assigned project")
	// Parent erasure remains the explicit retention boundary even though
	// directly deleting the assignment is forbidden. The organization's
	// projects go first: they now belong to it (#4882).
	if _, err := pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, project.ID); err != nil {
		t.Fatalf("project erasure: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, owner.PersonalOrg.ID); err != nil {
		t.Fatalf("assignment organization erasure: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_standard_assignments WHERE id = $1`, assignmentID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("assignment retained after organization erasure: count=%d err=%v", remaining, err)
	}
}
