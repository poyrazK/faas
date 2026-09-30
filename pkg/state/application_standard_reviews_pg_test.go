//go:build !no_pg

package state

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
)

func TestPgApplicationStandardReviewEmptyProject(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	standardReviewEmptyProject(t, NewPgStore(pool), func(a appstandards.Assignment, actor string) {
		if _, err := pool.Exec(ctx, `INSERT INTO application_standard_assignments (id,org_id,scope,scope_id,standard_id,admission_version,active,created_by) VALUES ($1,$2,$3,$4,$5,$6,true,$7)`, a.ID, a.OrgID, a.Scope, a.ScopeID, a.StandardID, a.AdmissionVersion, actor); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardReviewBatchAccountQuota(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	standardReviewBatchAccountQuota(t, NewPgStore(pool))
}

func TestPgApplicationStandardReviewSavedAdoptions(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	standardReviewSavedAdoptions(t, NewPgStore(pool), func(a appstandards.Assignment, actor string, active bool) {
		if _, err := pool.Exec(ctx, `INSERT INTO application_standard_assignments (id,org_id,scope,scope_id,standard_id,admission_version,active,created_by)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id) DO UPDATE SET admission_version = EXCLUDED.admission_version,
              active = EXCLUDED.active, revision = application_standard_assignments.revision + 1`, a.ID, a.OrgID, a.Scope, a.ScopeID, a.StandardID, a.AdmissionVersion, active, actor); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardReviews(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	standardReviewLifecycle(t, NewPgStore(pool))
	for _, query := range []string{`UPDATE application_standard_review_plans SET approval_hash = repeat('0',64)`, `DELETE FROM application_standard_review_plans`} {
		_, err := pool.Exec(ctx, query)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "application_standard_review_immutable" {
			t.Fatalf("immutable review: %v", err)
		}
	}
	store := NewPgStore(pool)
	actor, err := store.AccountByEmail(ctx, "review-owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, Org{Slug: "review-retention-org", Name: "Review retention", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, actor.ID, OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	v, err := store.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: org.ID, ActorID: actor.ID, Slug: "retention-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.PreviewApplicationStandardAssignment(ctx, org.ID, actor.ID, ApplicationStandardReviewRequest{Scope: "organization", ScopeID: org.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	operationID := uuid.NewString()
	// Private fixtures exercise the history fences before a public activation
	// endpoint exists. Worker state cannot rewrite the originally approved body.
	if _, err := pool.Exec(ctx, `INSERT INTO application_standard_assignments (id, org_id, scope, scope_id, standard_id, admission_version, active, created_by) VALUES ($1,$2,'organization',$2,$3,1,true,$4)`, p.Request.AssignmentID, org.ID, v.StandardID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO application_standard_operations (id,org_id,plan_id,assignment_id,approval_hash,approved_by,batch_size) VALUES ($1,$2,$3,$4,$5,$6,1)`, operationID, org.ID, p.ID, p.Request.AssignmentID, p.ApprovalHash, actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO application_standard_operation_targets (operation_id,app_id,position,approved_app) VALUES ($1,$2,0,'{}')`, operationID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ query, constraint string }{
		{`UPDATE application_standard_operations SET batch_size = 2`, "application_standard_operation_intent_immutable"},
		{`DELETE FROM application_standard_operations`, "application_standard_operation_intent_immutable"},
		{`UPDATE application_standard_operation_targets SET approved_app = '{"changed":true}'`, "application_standard_target_intent_immutable"},
		{`DELETE FROM application_standard_operation_targets`, "application_standard_target_intent_immutable"},
	} {
		_, err := pool.Exec(ctx, check.query)
		var failure *pgconn.PgError
		if !errors.As(err, &failure) || failure.ConstraintName != check.constraint {
			t.Fatalf("operation history fence: %v", err)
		}
	}
	// Actor erasure retains company review and approval attribution, without
	// holding account foreign keys or contact information in the review.
	for _, query := range []string{
		`DELETE FROM app_trusted_signers WHERE account_id IN (SELECT id FROM accounts WHERE email = 'review-owner@example.com')`,
		`DELETE FROM deployments WHERE app_id IN (SELECT id FROM apps WHERE account_id IN (SELECT id FROM accounts WHERE email = 'review-owner@example.com'))`,
		`DELETE FROM apps WHERE account_id IN (SELECT id FROM accounts WHERE email = 'review-owner@example.com')`,
		`DELETE FROM orgs WHERE personal_owner_account_id IN (SELECT id FROM accounts WHERE email = 'review-owner@example.com')`,
		`DELETE FROM accounts WHERE email = 'review-owner@example.com'`,
	} {
		if _, err := pool.Exec(ctx, query); err != nil {
			t.Fatalf("review actor erasure: %v", err)
		}
	}
	retained, err := store.GetApplicationStandardReviewPlan(ctx, org.ID, p.ID)
	if err != nil || retained.CreatedBy != actor.ID || retained.ApprovalHash != p.ApprovalHash {
		t.Fatalf("review provenance lost: %+v %v", retained, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, org.ID); err != nil {
		t.Fatalf("review organization erasure: %v", err)
	}
	for _, check := range []struct{ table, query string }{
		{"application_standard_review_plans", `SELECT count(*) FROM application_standard_review_plans`},
		{"application_standard_operations", `SELECT count(*) FROM application_standard_operations`},
		{"application_standard_operation_targets", `SELECT count(*) FROM application_standard_operation_targets`},
	} {
		var count int
		if err := pool.QueryRow(ctx, check.query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retained erased %s: %d %v", check.table, count, err)
		}
	}
}
