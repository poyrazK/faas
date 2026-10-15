//go:build !no_pg

package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func standardOperationPGStore(t *testing.T) (*PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return NewPgStore(pool), pool
}

func TestPgApplicationStandardConcurrentApproval(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan struct{})
	type result struct {
		op  ApplicationStandardOperation
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			op, err := s.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
			results <- result{op, err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.op.ID != b.op.ID {
		t.Fatalf("concurrent approval: %s %s %v %v", a.op.ID, b.op.ID, a.err, b.err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE kind='application_standard.approved'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate audit: %d %v", count, err)
	}
}

func TestPgApplicationStandardProjectApprovalMembershipRace(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, Project{AccountID: f.owner.Account.ID, Slug: "approval-empty-project"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "approval-project-foreign@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	preview := func() ApplicationStandardReviewPlan {
		t.Helper()
		p, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, ApplicationStandardReviewRequest{Scope: "project", ScopeID: project.ID, StandardID: f.version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := preview()
	// A foreign member that wins the project parent first makes the review
	// busy, then stale after it commits; no cross-org assignment is written.
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(ctx)
	var foreignApp string
	if err := writer.QueryRow(ctx, `INSERT INTO apps (account_id,org_id,slug,ram_mb,project_id,workload_name) VALUES ($1,$2,'foreign-project-first',128,$3,'foreign') RETURNING id::text`, foreign.Account.ID, foreign.PersonalOrg.ID, project.ID).Scan(&foreignApp); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewBusy) {
		t.Fatalf("foreign membership was not fenced: %v", err)
	}
	assertStandardApprovalNoWrites(t, t.Context(), pool)
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewStale) {
		t.Fatalf("foreign membership was assigned: %v", err)
	}
	assertStandardApprovalNoWrites(t, t.Context(), pool)
	if _, err := pool.Exec(ctx, `DELETE FROM apps WHERE id=$1`, foreignApp); err != nil {
		t.Fatal(err)
	}
	p = preview()
	// Conversely, an in-flight approved assignment holds the project fence.
	// The blocked foreign insert must recheck ownership after commit.
	locked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback(ctx)
	if _, err := sqlc.New().LockApplicationStandardApprovalOrg(ctx, locked, mustPgUUID(f.owner.PersonalOrg.ID)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := lockStandardReviewInputs(ctx, locked, f.owner.PersonalOrg.ID, f.owner.Account.ID, p.Request)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := buildStandardReview(snapshot, p.Request, p.ID, p.CreatedBy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStandardReviewHash(p, fresh, p.ApprovalHash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	op, err := newStandardOperation(fresh, f.owner.Account.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := persistStandardApproval(ctx, locked, p.Request, op); err != nil {
		t.Fatal(err)
	}
	blocked, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Rollback(ctx)
	var pid int
	if err := blocked.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := blocked.Exec(ctx, `INSERT INTO apps (account_id,org_id,slug,ram_mb,project_id,workload_name) VALUES ($1,$2,'foreign-project-late',128,$3,'late')`, foreign.Account.ID, foreign.PersonalOrg.ID, project.ID)
		result <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("foreign insert did not wait on the project fence")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := locked.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var pgErr *pgconn.PgError
	if err := <-result; !errors.As(err, &pgErr) || pgErr.ConstraintName != "application_standard_scope_owner" {
		t.Fatalf("foreign app crossed approved scope: %v", err)
	}
}

func TestPgApplicationStandardApprovalExpired(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardApprovalExpired(t, s, func(p ApplicationStandardReviewPlan) {
		request, _ := json.Marshal(p.Request)
		inputs, _ := json.Marshal(p.approvalInputs)
		apps, _ := json.Marshal(p.Applications)
		blockers, _ := json.Marshal(p.Blockers)
		err := sqlc.New().InsertApplicationStandardReviewPlan(context.Background(), pool, sqlc.InsertApplicationStandardReviewPlanParams{ID: mustPgUUID(p.ID), OrgID: mustPgUUID(p.OrgID), CreatedBy: mustPgUUID(p.CreatedBy), Request: request, ApprovalInputs: inputs, ApprovalHash: p.ApprovalHash, Applications: apps, Blockers: blockers, CreatedAt: pgtype.Timestamptz{Time: p.CreatedAt, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: p.ExpiresAt, Valid: true}})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertStandardApprovalNoWrites(t, t.Context(), pool)
}

func TestPgApplicationStandardReviewRetainedArtifacts(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardReviewRetainedArtifacts(t, s)
}

func TestPgApplicationStandardApprovalLifecycle(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardApprovalLifecycle(t, s, func(id string) {
		// Terminal fixture only, to exercise a later reviewed version. Actual
		// worker completion will require consumer evidence, not this update.
		if _, err := pool.Exec(context.Background(), `UPDATE application_standard_operations SET state = 'completed' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	})
	var operations, targets, audits int
	if err := pool.QueryRow(context.Background(), `SELECT
      (SELECT count(*) FROM application_standard_operations),
      (SELECT count(*) FROM application_standard_operation_targets),
      (SELECT count(*) FROM audit_log WHERE kind = 'application_standard.approved')`).Scan(&operations, &targets, &audits); err != nil || operations != 3 || targets != 9 || audits != 3 {
		t.Fatalf("atomic history: %d %d %d %v", operations, targets, audits, err)
	}
}

func TestPgApplicationStandardApprovalRejectsChangedInputs(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardApprovalRejectsChangedInputs(t, s, func() { assertStandardApprovalNoWrites(t, t.Context(), pool) })
}

func assertStandardApprovalNoWrites(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM application_standard_assignments)
      + (SELECT count(*) FROM application_standard_operations) + (SELECT count(*) FROM application_standard_operation_targets)
      + (SELECT count(*) FROM audit_log WHERE kind = 'application_standard.approved')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected approval wrote intent/history: %d %v", count, err)
	}
}

func TestPgApplicationStandardApprovalFailureRollsBack(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_standard_approval_audit() RETURNS trigger LANGUAGE plpgsql AS $$
      BEGIN IF NEW.kind = 'application_standard.approved' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END; $$;
      CREATE TRIGGER reject_standard_approval_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_standard_approval_audit();`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash); err == nil {
		t.Fatal("injected failure committed")
	}
	assertStandardApprovalNoWrites(t, t.Context(), pool)
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_standard_approval_audit ON audit_log`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash); err != nil {
		t.Fatal(err)
	}
}

func TestPgApplicationStandardApprovalWriterFirst(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO app_log_drains (app_id,account_id,kind,target_url,enabled) VALUES ($1,$2,'http_json','https://concurrent.example/logs',false)`, f.apps[0].ID, f.owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewBusy) {
		t.Fatalf("in-flight writer bypassed parent fences: %v", err)
	}
	assertStandardApprovalNoWrites(t, t.Context(), pool)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewStale) {
		t.Fatalf("committed writer did not stale review: %v", err)
	}
	assertStandardApprovalNoWrites(t, t.Context(), pool)
}

func TestPgApplicationStandardApprovalParentFences(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: f.apps[0].ID, Kind: DeploymentKindImage, Status: DeploySuperseded})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, dep.ID, DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	// A drain in another scope still contributes to the same account quota.
	outside, err := s.CreateApp(ctx, App{AccountID: f.owner.Account.ID, OrgID: f.owner.PersonalOrg.ID, Slug: "quota-outside", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	outsideDrain, err := s.CreateAppLogDrain(ctx, AppLogDrain{AppID: outside.ID, AccountID: f.owner.Account.ID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://outside.example/logs", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, ApplicationStandardReviewRequest{Scope: "application", ScopeID: f.apps[0].ID, StandardID: f.version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name, sql string
		args      []any
	}{
		{"drain insert", `INSERT INTO app_log_drains (app_id,account_id,kind,target_url) VALUES ($1,$2,'http_json','https://fenced.example/logs')`, []any{f.apps[0].ID, f.owner.Account.ID}},
		{"outside quota", `INSERT INTO app_log_drains (app_id,account_id,kind,target_url,enabled) VALUES ($1,$2,'http_json','https://quota.example/logs',false)`, []any{outside.ID, f.owner.Account.ID}},
		{"outside quota update", `UPDATE app_log_drains SET target_url='https://changed.example/logs' WHERE id=$1`, []any{outsideDrain.ID}},
		{"outside quota delete", `DELETE FROM app_log_drains WHERE id=$1`, []any{outsideDrain.ID}},
		{"signer insert", `INSERT INTO app_trusted_signers (app_id,account_id,signer_name,cosign_public_key,added_by_account_id) VALUES ($1,$2,'ci',decode(repeat('ab',100),'hex'),$2)`, []any{f.apps[0].ID, f.owner.Account.ID}},
		{"artifact update", `UPDATE deployments SET rootfs_key='fenced/rootfs',rootfs_bytes=100 WHERE id=$1`, []any{dep.ID}},
		{"sidecar insert", `INSERT INTO deployment_sidecar_layers (deployment_id,sidecar_name,storage_key,bytes,content_digest) VALUES ($1,'agent','fenced/agent',100,$2)`, []any{dep.ID, "sha256:" + strings.Repeat("a", 64)}},
		{"retained artifact becomes live", `INSERT INTO instances (id,app_id,deployment_id,state,mode,ram_mb,node_id) VALUES ($1,$2,$3,'running','normal',128,(SELECT id FROM compute_nodes ORDER BY id LIMIT 1))`, []any{uuid.NewString(), f.apps[0].ID, dep.ID}},
	} {
		t.Run(change.name, func(t *testing.T) {
			locked, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer locked.Rollback(ctx)
			if _, err := sqlc.New().LockApplicationStandardApprovalOrg(ctx, locked, mustPgUUID(f.owner.PersonalOrg.ID)); err != nil {
				t.Fatal(err)
			}
			if _, err := lockStandardReviewInputs(ctx, locked, f.owner.PersonalOrg.ID, f.owner.Account.ID, p.Request); err != nil {
				t.Fatal(err)
			}
			writer, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback(ctx)
			if _, err := writer.Exec(ctx, `SET LOCAL lock_timeout = '100ms'`); err != nil {
				t.Fatal(err)
			}
			_, err = writer.Exec(ctx, change.sql, change.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
				t.Fatalf("writer was not fenced: %v", err)
			}
			_ = writer.Rollback(ctx)
			_ = locked.Rollback(ctx)
			// Verify the same write is valid once the approval releases its
			// authority, so an unrelated CHECK failure cannot pass this test.
			released, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer released.Rollback(ctx)
			if _, err := released.Exec(ctx, change.sql, change.args...); err != nil {
				t.Fatalf("write after release: %v", err)
			}
		})
	}
}

func TestPgApplicationStandardApprovalReenrollmentRevokesLease(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	app := f.apps[0]
	if _, err := pool.Exec(ctx, `UPDATE app_application_standards SET lease_owner='old-worker',lease_generation=3,lease_until=now()+interval '1 minute' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	// Consumer bookkeeping is outside desired intent and leaves authority alone.
	if _, err := pool.Exec(ctx, `UPDATE app_application_standards SET updated_at=now() WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	var owner string
	var generation int64
	var until *time.Time
	read := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT lease_owner,lease_generation,lease_until FROM app_application_standards WHERE app_id=$1`, app.ID).Scan(&owner, &generation, &until); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if owner != "old-worker" || generation != 3 || until == nil {
		t.Fatal("heartbeat revoked authority")
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET status='deleted' WHERE id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET status='active' WHERE id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	read()
	if owner != "" || generation != 4 || until != nil {
		t.Fatalf("restore retained stale authority: %q %d %v", owner, generation, until)
	}
	if _, err := pool.Exec(ctx, `UPDATE app_application_standards SET lease_generation=2 WHERE app_id=$1`, app.ID); err == nil {
		t.Fatal("generation regressed")
	}
}

func TestPgApplicationStandardArtifactWriterFences(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: f.apps[0].ID, Kind: DeploymentKindImage, Status: DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deployment_sidecar_layers (deployment_id,sidecar_name,storage_key,bytes,content_digest) VALUES ($1,'agent','agent/rootfs',100,$2)`, dep.ID, "sha256:"+strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	var instanceID string
	if err := pool.QueryRow(ctx, `INSERT INTO instances (app_id,deployment_id,state,ram_mb,node_id) VALUES ($1,$2,'stopped',128,(SELECT id FROM compute_nodes ORDER BY id LIMIT 1)) RETURNING id::text`, f.apps[0].ID, dep.ID).Scan(&instanceID); err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.plan.Request)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name, sql string
		arg       any
	}{
		{"artifact metadata", `UPDATE deployments SET rootfs_key='new/rootfs',rootfs_bytes=100 WHERE id=$1`, dep.ID},
		{"sidecar metadata", `UPDATE deployment_sidecar_layers SET bytes=200 WHERE deployment_id=$1`, dep.ID},
		// Cleanup must retain the legacy nonwaiting child-fence behaviour.
		// Entry into RUNNING is now admission, with its own fail-fast parent
		// fence covered by TestPgInstanceApplicationStandardCaptureInputContention.
		{"instance cleanup", `UPDATE instances SET state='parked' WHERE id=$1`, instanceID},
	} {
		t.Run(change.name, func(t *testing.T) {
			// Legacy app-first transactions may later acquire a child row.
			// A metadata writer must finish without waiting back on that app.
			parent, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Rollback(ctx)
			if _, err := parent.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, f.apps[0].ID); err != nil {
				t.Fatal(err)
			}
			writer, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback(ctx)
			if _, err := writer.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Exec(ctx, change.sql, change.arg); err != nil {
				t.Fatalf("metadata writer added a reverse parent wait: %v", err)
			}
			_ = parent.Rollback(ctx)
			// An instance/sidecar update has no deployment FK-key change, so
			// only its advisory fence can exclude a concurrent approval.
			if _, err := s.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewBusy) {
				t.Fatalf("in-flight artifact writer bypassed approval: %v", err)
			}
			assertStandardApprovalNoWrites(t, ctx, pool)
		})
	}
	_, err = pool.Exec(ctx, `UPDATE deployments SET app_id=$1 WHERE id=$2`, f.apps[1].ID, dep.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "application_standard_artifact_identity" {
		t.Fatalf("artifact crossed the reviewed lock set: %v", err)
	}
}

func TestPgApplicationStandardControlWriterFences(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	drain, err := s.CreateAppLogDrain(ctx, AppLogDrain{AppID: f.apps[0].ID, AccountID: f.owner.Account.ID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://control.example/logs", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO app_trusted_signers (app_id,account_id,signer_name,cosign_public_key,added_by_account_id) VALUES ($1,$2,'ci',decode(repeat('ab',100),'hex'),$2)`, f.apps[0].ID, f.owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.plan.Request)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name, sql string
		arg       any
	}{
		{"drain update", `UPDATE app_log_drains SET target_url='https://changed.example/logs' WHERE id=$1`, drain.ID},
		{"drain delete", `DELETE FROM app_log_drains WHERE id=$1`, drain.ID},
		{"signer update", `UPDATE app_trusted_signers SET cosign_public_key=decode(repeat('cd',100),'hex') WHERE app_id=$1`, f.apps[0].ID},
		{"signer delete", `DELETE FROM app_trusted_signers WHERE app_id=$1`, f.apps[0].ID},
	} {
		t.Run(change.name, func(t *testing.T) {
			parent, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Rollback(ctx)
			if _, err := parent.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, f.apps[0].ID); err != nil {
				t.Fatal(err)
			}
			writer, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback(ctx)
			if _, err := writer.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Exec(ctx, change.sql, change.arg); err != nil {
				t.Fatalf("control writer added a reverse parent wait: %v", err)
			}
			_ = parent.Rollback(ctx)
			if _, err := s.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewBusy) {
				t.Fatalf("in-flight control writer bypassed approval: %v", err)
			}
			assertStandardApprovalNoWrites(t, ctx, pool)
		})
	}
	foreign, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "control-other-account@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE apps SET account_id=$1 WHERE id=$2`, foreign.Account.ID, f.apps[0].ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "application_standard_app_account_identity" {
		t.Fatalf("billing identity changed behind quota fence: %v", err)
	}
}
