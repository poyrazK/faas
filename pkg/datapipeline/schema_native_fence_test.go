package datapipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The direct fault injection bypasses worker ticks, which normally retry a
// transient database lock refusal. Retain the SQL lock budget and overall
// test deadline; retry only 55P03 before the injected final lease fence.
func assertNativeSchemaLeaseFence(t *testing.T, ctx context.Context, approved Pipeline, source, target *pgx.Conn) error {
	t.Helper()
	publisher, err := pgxpool.New(ctx, source.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	var owner, beforeHash, beforeChange, beforePhase, beforeBarrier string
	var beforeSpec []byte
	if err = target.QueryRow(ctx, `SELECT tableowner FROM pg_tables WHERE schemaname='public' AND tablename='orders'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow(ctx, `SELECT schema_hash,schema_spec,schema_change,schema_phase,barrier::text FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, approved.ID, approved.Generation).Scan(&beforeHash, &beforeSpec, &beforeChange, &beforePhase, &beforeBarrier); err != nil {
		t.Fatal(err)
	}
	assertRolledBack := func() {
		t.Helper()
		var clean bool
		if err := target.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='public.orders'::regclass AND attname IN ('extra_note','also_note') AND NOT attisdropped) AND schema_hash=$3 AND schema_spec=$4::jsonb AND schema_change=$5 AND schema_phase=$6 AND barrier=$7::pg_lsn FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, approved.ID, approved.Generation, beforeHash, beforeSpec, beforeChange, beforePhase, beforeBarrier).Scan(&clean); err != nil || !clean {
			t.Fatal("schema attempt left partial target DDL or receipt changes", err)
		}
	}
	attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fenced := false
	for {
		err = applySchema(attempt, approved, publisher, target, owner, *approved.SchemaChange, func(current context.Context) error {
			fenced = true
			var staged bool
			if err := target.QueryRow(current, `SELECT (SELECT count(*) FROM pg_attribute WHERE attrelid='public.orders'::regclass AND attname IN ('extra_note','also_note') AND NOT attisdropped)=2 AND schema_change=$2 AND schema_phase='verifying' FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, approved.ID, approved.SchemaChange.ID).Scan(&staged); err != nil || !staged {
				t.Fatal("lease fence did not observe both uncommitted columns and staged receipt", err)
			}
			return ErrConflict
		})
		assertRolledBack()
		var lockErr *pgconn.PgError
		if !errors.As(err, &lockErr) || lockErr.Code != "55P03" || fenced || attempt.Err() != nil {
			if errors.Is(err, ErrConflict) && !fenced {
				t.Fatal("schema test returned conflict without reaching the final lease fence")
			}
			return err
		}
		t.Logf("retrying schema fixture lock contention before lease fence: %s", lockErr.Code)
		select {
		case <-attempt.Done():
			return attempt.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
