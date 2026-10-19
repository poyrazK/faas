//go:build !no_pg

// adr: 595. Environment review inputs serialize with raw and typed writers.
package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPgApplicationStandardEnvironmentReview(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardEnvironmentReviewRevisions(t, s)
}

func TestPgApplicationStandardEnvironmentReviewPinAndBody(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardEnvironmentReviewPinAndBody(t, s, func(f standardEnvironmentReviewFixture, specID string) {
		if _, err := pool.Exec(t.Context(), `UPDATE project_environment_workload_deployment_specs SET spec_id=$2 WHERE deployment_id=$1`, f.dep.ID, specID); err != nil {
			t.Fatal(err)
		}
	}, func(f standardEnvironmentReviewFixture) {
		if _, err := pool.Exec(t.Context(), `UPDATE project_environment_workload_specs SET settings=jsonb_set(settings::jsonb,'{ram_mb}','1024')::json WHERE id=$1`, f.spec.ID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardEnvironmentMaterializationReview(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardEnvironmentMaterializationReview(t, s)
}

func TestPgApplicationStandardEnvironmentMaterializationCorruptBody(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardEnvironmentMaterializationDrift(t, s, func(f standardEnvironmentReviewFixture) {
		if _, err := pool.Exec(t.Context(), `UPDATE project_environment_workload_specs SET settings=jsonb_set(settings::jsonb,'{ram_mb}','1024')::json WHERE id=$1`, f.spec.ID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardEnvironmentReviewWriterFences(t *testing.T) {
	for _, name := range []string{"head", "settings", "protection", "first head", "deployment pin"} {
		t.Run(name, func(t *testing.T) {
			s, pool := standardOperationPGStore(t)
			f := newStandardEnvironmentReviewFixture(t, s)
			query, arg := `UPDATE project_environment_workload_heads SET spec_id=spec_id WHERE app_id=$1`, f.app.ID
			switch name {
			case "settings":
				query, arg = `UPDATE project_environment_workload_specs SET settings=settings WHERE id=$1`, f.spec.ID
			case "protection":
				query, arg = `UPDATE project_environments SET protected=protected WHERE id=$1`, f.env.ID
			case "first head":
				if _, err := pool.Exec(t.Context(), `DELETE FROM project_environment_workload_heads WHERE app_id=$1`, f.app.ID); err != nil {
					t.Fatal(err)
				}
				query, arg = `INSERT INTO project_environment_workload_heads (environment_id,app_id,spec_id) SELECT environment_id,app_id,id FROM project_environment_workload_specs WHERE id=$1`, f.spec.ID
			case "deployment pin":
				query, arg = `UPDATE project_environment_workload_deployment_specs SET spec_id=spec_id WHERE deployment_id=$1`, f.dep.ID
			}
			p := f.preview(t, s)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			writer, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(t.Context()) }()
			if _, err := writer.Exec(ctx, query, arg); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ApproveApplicationStandardReview(ctx, f.app.OrgID, f.app.AccountID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewBusy) {
				t.Fatalf("in-flight environment writer crossed approval: %v", err)
			}
			assertStandardApprovalNoWrites(t, ctx, pool)
			if err := writer.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			// Check the reverse direction, including insertion into an empty head set.
			approval, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = approval.Rollback(t.Context()) }()
			if _, err := lockStandardReviewInputs(ctx, approval, f.app.OrgID, f.app.AccountID, p.Request); err != nil {
				t.Fatal(err)
			}
			writer, err = pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(t.Context()) }()
			if _, err := writer.Exec(ctx, `SET LOCAL lock_timeout='150ms'`); err != nil {
				t.Fatal(err)
			}
			_, err = writer.Exec(ctx, query, arg)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
				t.Fatalf("environment writer crossed approved snapshot: %v", err)
			}
			_ = writer.Rollback(ctx)
			if err := approval.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ApproveApplicationStandardReview(ctx, f.app.OrgID, f.app.AccountID, p.ID, p.ApprovalHash); err != nil {
				t.Fatalf("released review did not retry: %v", err)
			}
		})
	}
}

func TestPgApplicationStandardEnvironmentMaterializationWriterFence(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardEnvironmentReviewFixture(t, s)
	p := f.preview(t, s)
	if _, err := s.ApproveApplicationStandardReview(t.Context(), f.app.OrgID, f.app.AccountID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "environment-fence-worker")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(t.Context()) }()
	if _, err := writer.Exec(t.Context(), `UPDATE project_environment_workload_specs SET settings=settings WHERE id=$1`, f.spec.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, c); !errors.Is(err, ErrApplicationStandardReviewBusy) {
		t.Fatalf("materialization crossed private settings writer: %v", err)
	}
	if err := writer.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	o, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c)
	if err != nil || len(o.Targets) != 1 || o.Targets[0].State != "persisted" {
		t.Fatalf("released materialization did not retry: %+v %v", o, err)
	}
}
