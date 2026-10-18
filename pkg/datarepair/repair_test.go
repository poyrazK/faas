package datarepair

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// ADR-20261016: mechanical eligibility, atomic repair, and durable receipts.
type successfulRehearsal struct{}

func TestSelectiveRepairStablePlanIdentity(t *testing.T) {
	e, s, source := fixture(t, 2)
	id := uuid.NewString()
	p, err := e.Prepare(t.Context(), s, source, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(t.Context(), `UPDATE products SET category='human' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	replay, err := e.Prepare(t.Context(), s, source, id)
	if err != nil || replay.ID != p.ID || replay.Digest != p.Digest || !replay.ExpiresAt.Equal(p.ExpiresAt) || replay.Candidates != 2 {
		t.Fatalf("plan replay refreshed eligibility: %+v %v", replay, err)
	}
	other := s
	other.AppID = uuid.NewString()
	if _, err = e.Prepare(t.Context(), other, source, id); !errors.Is(err, ErrConflict) {
		t.Fatalf("plan identity crossed scope: %v", err)
	}
}

func (successfulRehearsal) Rehearse(_ context.Context, p Plan) (Evidence, error) {
	return Evidence{Passed: true, PlanDigest: p.Digest, SnapshotSHA256: strings.Repeat("a", 64), Runner: "test-only", ChecksSHA256: p.ChecksSHA256}, nil
}

func fixture(t *testing.T, n int) (*Engine, Scope, string) {
	t.Helper()
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE products(id bigint PRIMARY KEY,category text NOT NULL CHECK(category IN ('original','bad','human')),quantity integer NOT NULL DEFAULT 1);`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT gregale_repair.enroll('products'::regclass,ARRAY['category'])`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,category) SELECT generate_series(1,$1),'original'`, n); err != nil {
		t.Fatal(err)
	}
	s := Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	op := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = Capture(ctx, tx, s, op, "buggy-import-v1", "products", func(tx pgx.Tx) error { _, err := tx.Exec(ctx, `UPDATE products SET category='bad'`); return err })
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return &Engine{Pool: pool, Rehearser: successfulRehearsal{}}, s, op
}

func approved(t *testing.T, e *Engine, s Scope, op string) Plan {
	t.Helper()
	p, err := e.Plan(t.Context(), s, op)
	if err != nil {
		t.Fatal(err)
	}
	p, err = e.Rehearse(t.Context(), s, p.ID, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	p, err = e.Approve(t.Context(), s, p.ID, p.Digest, "reviewer@example.test")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSelectiveRepair50040(t *testing.T) {
	e, s, op := fixture(t, 500)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	p := approved(t, e, s, op)
	if p.Candidates != 460 || p.Conflicts != 40 || len(p.Items) != 500 {
		t.Fatalf("unexpected selection: %d %d %d", p.Candidates, p.Conflicts, len(p.Items))
	}
	repairID := uuid.NewString()
	done, err := e.Apply(ctx, s, p.ID, p.Digest, repairID, "repair-executor-v1")
	if err != nil {
		t.Fatal(err)
	}
	if done.RepairOperationID == nil || *done.RepairOperationID != repairID || done.AppliedAt == nil {
		t.Fatal("missing durable receipt")
	}
	var restored, human, journal int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='original' AND gregale_row_version=3),count(*) FILTER(WHERE category='human'),(SELECT count(*) FROM gregale_repair.changes WHERE operation_id=$1) FROM products`, repairID).Scan(&restored, &human, &journal); err != nil || restored != 460 || human != 40 || journal != 460 {
		t.Fatalf("restored=%d human=%d journal=%d err=%v", restored, human, journal, err)
	}
	// A lost response is reconciled even after the approval window has expired.
	_, err = e.Pool.Exec(ctx, `UPDATE gregale_repair.plans SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apply(ctx, s, p.ID, p.Digest, repairID, "repair-executor-v1"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apply(ctx, s, p.ID, p.Digest, uuid.NewString(), "repair-executor-v1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second identity reapplied plan: %v", err)
	}
}

func TestSelectiveRepairStaleApprovalRollsBackEveryRow(t *testing.T) {
	e, s, op := fixture(t, 20)
	p := approved(t, e, s, op)
	ctx := t.Context()
	// The final lock target becomes stale so earlier inverse writes must roll back.
	last := p.Items[len(p.Items)-1].RowIdentity
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE gregale_row_identity=$1`, last); err != nil {
		t.Fatal(err)
	}
	repairID := uuid.NewString()
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, repairID, "repair-v1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale approval: %v", err)
	}
	var restored, recorded int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM products WHERE category='original'),(SELECT count(*) FROM gregale_repair.operations WHERE id=$1)`, repairID).Scan(&restored, &recorded); err != nil || restored != 0 || recorded != 0 {
		t.Fatalf("partial repair escaped: %d %d %v", restored, recorded, err)
	}
}

func TestSelectiveRepairDetectsABAAndRecreatedKeys(t *testing.T) {
	e, s, op := fixture(t, 3)
	ctx := t.Context()
	for _, sql := range []string{`UPDATE products SET category='human' WHERE id=1`, `UPDATE products SET category='bad' WHERE id=1`, `DELETE FROM products WHERE id=2`, `INSERT INTO products(id,category) VALUES(2,'original')`, `UPDATE products SET category='bad' WHERE id=2`} {
		if _, err := e.Pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	p, err := e.Plan(ctx, s, op)
	if err != nil || p.Candidates != 1 || p.Conflicts != 1 || p.Unavailable != 1 {
		t.Fatalf("ABA/recreate eligibility: %+v %v", p, err)
	}
}

func TestSelectiveRepairCaptureCannotCommitUnfinished(t *testing.T) {
	e, s, _ := fixture(t, 1)
	ctx := t.Context()
	op := uuid.NewString()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = Capture(ctx, tx, s, op, "second-operation", "products", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE products SET category='human'`); err != nil {
			return err
		}
		return errors.New("application validation failed")
	})
	if err == nil {
		t.Fatal("callback failure lost")
	}
	// Even a caller that mistakenly swallows the error cannot commit the journal.
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("unfinished capture committed")
	}
	var category string
	var count int
	if err := e.Pool.QueryRow(ctx, `SELECT category,(SELECT count(*) FROM gregale_repair.operations WHERE id=$1) FROM products`, op).Scan(&category, &count); err != nil || category != "bad" || count != 0 {
		t.Fatalf("unfinished operation persisted: %s %d %v", category, count, err)
	}
}

func TestSelectiveRepairRequiresDeclaredWritesAndScope(t *testing.T) {
	e, s, op := fixture(t, 2)
	ctx := t.Context()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = Capture(ctx, tx, s, uuid.NewString(), "invalid", "products", func(tx pgx.Tx) error { _, err := tx.Exec(ctx, `UPDATE products SET quantity=2`); return err })
	_ = tx.Rollback(ctx)
	if err == nil {
		t.Fatal("undeclared update captured")
	}
	other := s
	other.Environment = "staging"
	if _, err := e.Plan(ctx, other, op); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-scope history read: %v", err)
	}
	p, err := e.Plan(ctx, s, op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Approve(ctx, s, p.ID, p.Digest, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("approval without rehearsal: %v", err)
	}
	if _, err = e.Get(ctx, other, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-scope plan read: %v", err)
	}
	if _, err = e.Pool.Exec(ctx, `SELECT gregale_repair.apply_plan($1::uuid,$2::jsonb,NULL,$3::uuid,'repair')`, p.ID, scopeJSON(s), uuid.NewString()); err == nil {
		t.Fatal("NULL digest bypassed guard")
	}
}

func TestSelectiveRepairApplicationChecksAndImmutableEvidence(t *testing.T) {
	e, s, op := fixture(t, 4)
	ctx := t.Context()
	for _, query := range []string{`UPDATE gregale_repair.changes SET before_values='{}' WHERE operation_id=$1`, `UPDATE gregale_repair.operations SET finalized=false WHERE id=$1`, `DELETE FROM gregale_repair.changes WHERE operation_id=$1`} {
		if _, err := e.Pool.Exec(ctx, query, op); err == nil {
			t.Fatal("committed evidence could be modified")
		}
	}
	e.CheckSQL = `SELECT false`
	p := approved(t, e, s, op)
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, uuid.NewString(), "repair-v1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("failed application check committed: %v", err)
	}
	var bad int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE category='bad'`).Scan(&bad); err != nil || bad != 4 {
		t.Fatalf("check did not roll back repair: %d %v", bad, err)
	}
	e.CheckSQL = `SELECT true`
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, uuid.NewString(), "repair-v1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("check policy changed under approval: %v", err)
	}
	p = approved(t, e, s, op)
	repairID := uuid.NewString()
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, repairID, "repair-v1"); err != nil {
		t.Fatal(err)
	}
	e.CheckSQL = `SELECT false`
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, repairID, "repair-v1"); err != nil {
		t.Fatalf("receipt replay reran changed checks: %v", err)
	}
}

func TestSelectiveRepairCaptureLimitAndRepeatedUpdates(t *testing.T) {
	t.Run("repeated row", func(t *testing.T) {
		e, s, _ := fixture(t, 1)
		ctx := t.Context()
		id := uuid.NewString()
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = Capture(ctx, tx, s, id, "repeated-updates", "products", func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE products SET category='human';UPDATE products SET category='original'`)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var before, after string
		var initial, final int
		if err = e.Pool.QueryRow(ctx, `SELECT before_values->>'category',after_values->>'category',before_version,after_version FROM gregale_repair.changes WHERE operation_id=$1`, id).Scan(&before, &after, &initial, &final); err != nil || before != "bad" || after != "original" || initial != 2 || final != 4 {
			t.Fatalf("first/final capture: %s %s %d %d %v", before, after, initial, final, err)
		}
	})
	t.Run("quota rollback", func(t *testing.T) {
		e, s, _ := fixture(t, 1)
		ctx := t.Context()
		if _, err := e.Pool.Exec(ctx, `INSERT INTO products(id,category) SELECT generate_series(2,1001),'original'`); err != nil {
			t.Fatal(err)
		}
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = Capture(ctx, tx, s, uuid.NewString(), "over-limit", "products", func(tx pgx.Tx) error { _, err := tx.Exec(ctx, `UPDATE products SET category='human'`); return err })
		_ = tx.Rollback(ctx)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "54000" {
			t.Fatalf("capture overflow: %v", err)
		}
		var human int
		if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE category='human'`).Scan(&human); err != nil || human != 0 {
			t.Fatalf("overflow committed partial import: %d %v", human, err)
		}
	})
}

func TestSelectiveRepairConcurrentApplyAndSchemaDrift(t *testing.T) {
	t.Run("one durable repair", func(t *testing.T) {
		e, s, op := fixture(t, 5)
		p := approved(t, e, s, op)
		id := uuid.NewString()
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := e.Apply(t.Context(), s, p.ID, p.Digest, id, "repair-v1")
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM gregale_repair.changes WHERE operation_id=$1`, id).Scan(&count); err != nil || count != 5 {
			t.Fatalf("repair executed twice: %d %v", count, err)
		}
	})
	t.Run("schema drift", func(t *testing.T) {
		e, s, op := fixture(t, 2)
		p := approved(t, e, s, op)
		if _, err := e.Pool.Exec(t.Context(), `ALTER TABLE products ADD COLUMN new_policy text`); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Apply(t.Context(), s, p.ID, p.Digest, uuid.NewString(), "repair-v1"); !errors.Is(err, ErrConflict) {
			t.Fatalf("schema drift accepted: %v", err)
		}
	})
}

func TestSelectiveRepairRealIsolatedRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, s, op := fixture(t, 500)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	cfg := e.Pool.Config().ConnConfig.Copy()
	dsn := cfg.ConnString()
	// pgtest changes Database after parsing the original URL.
	if at := strings.LastIndex(dsn, "/"); at >= 0 {
		dsn = dsn[:at+1] + cfg.Database + "?sslmode=disable"
	}
	e.Rehearser = DockerRehearser{SourceURL: dsn, DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP")}
	p := approved(t, e, s, op)
	var bad int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE category='bad'`).Scan(&bad); err != nil || bad != 460 {
		t.Fatalf("rehearsal modified source: %d %v", bad, err)
	}
	if p.Rehearsal == nil || p.Rehearsal.Runner != "docker-networkless-postgres16-v1" {
		t.Fatal("no real sandbox evidence")
	}
	if _, err := e.Apply(ctx, s, p.ID, p.Digest, uuid.NewString(), "repair-v1"); err != nil {
		t.Fatal(err)
	}
}
