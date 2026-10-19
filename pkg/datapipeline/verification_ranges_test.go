package datapipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestVerificationCompositeRangesSplitAndTrackReplicaWrites(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Release()
	target := acquired.Conn()
	p := Pipeline{AccountID: uuid.NewString()}
	p.ID = uuid.NewString()
	p.Generation = 1
	p.Revision = 1
	p.Tables = []string{"verification_case"}
	if _, err = pool.Exec(ctx, `CREATE TABLE public.verification_case(a text COLLATE "C",b timestamptz,payload bytea,PRIMARY KEY(a,b));
 INSERT INTO public.verification_case SELECT 's'||(i%10)::text,'2020-01-01T00:00:00Z'::timestamptz+i*interval '1 second',convert_to('payload '||i,'UTF8') FROM generate_series(1,10001) i;
 CREATE SCHEMA gregale_pipeline`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "COMMENT ON TABLE public.verification_case IS "+literal(marker(p))); err != nil {
		t.Fatal(err)
	}
	r, _, err := inspectSourceTable(ctx, pool, p.Tables[0])
	if err != nil {
		t.Fatal(err)
	}
	relations := []relation{r}
	intent, spec := verificationID(p, relations)
	storage, err := verificationSourceStorage(ctx, pool, relations)
	if err != nil {
		t.Fatal(err)
	}
	id := physicalVerificationID(intent, storage)
	c, err := startVerification(ctx, p, target, relations, id, intent, spec, storage, 0, func(_ context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	compare := func(dirty *verificationRange) {
		t.Helper()
		src, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = src.Rollback(ctx) }()
		dst, err := target.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = dst.Rollback(ctx) }()
		if err = verificationSetting(ctx, src); err != nil {
			t.Fatal(err)
		}
		if err = verificationSetting(ctx, dst); err != nil {
			t.Fatal(err)
		}
		moved, err := compareLockedVerificationRange(ctx, p, src, dst, r, c, 0, dirty)
		if err != nil || !moved {
			t.Fatal("composite comparison failed", moved, err, c.Progress)
		}
		if err = saveVerification(ctx, dst, p, c); err != nil {
			t.Fatal(err)
		}
		if err = dst.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for c.Table == 0 {
		compare(nil)
	}
	if c.Progress.Rows != 10001 || c.Progress.Ranges < 5 {
		t.Fatal("base scan skipped keys", c.Progress)
	}
	// Stored bounds must retain canonical text even if the connection's display settings change.
	if _, err = target.Exec(ctx, `SET timezone='Pacific/Honolulu';SET datestyle='SQL,DMY';SET session_replication_role=replica;
 INSERT INTO public.verification_case VALUES('s0','2020-01-01T00:00:10.5Z',decode('ff','hex'));RESET session_replication_role`); err != nil {
		t.Fatal(err)
	}
	pending, err := dirtyVerificationRange(ctx, p, target, r, 0)
	if err != nil || pending == nil {
		t.Fatal("replica write was not tracked", err)
	}
	// Grow a previously checked range beyond one batch, requiring a split.
	if _, err = pool.Exec(ctx, `INSERT INTO public.verification_case SELECT 's0','2020-01-01T00:00:00Z'::timestamptz+i*interval '1 millisecond',decode('01','hex') FROM generate_series(1,4000) i WHERE i%1000<>0`); err != nil {
		t.Fatal(err)
	}
	rangesBefore := c.Progress.Ranges
	for n := 0; n < 10; n++ {
		pending, err = dirtyVerificationRange(ctx, p, target, r, 0)
		if err != nil {
			t.Fatal(err)
		}
		if pending == nil {
			break
		}
		compare(pending)
	}
	if c.Progress.Ranges <= rangesBefore {
		t.Fatal("growing range did not split")
	}
	if dirty, err := countDirtyVerification(ctx, p, target, relations); err != nil || dirty != 0 {
		t.Fatal("split left dirty ranges", dirty, err)
	}
	checkCoverage := func(wantFailure bool) {
		t.Helper()
		tx, err := target.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err = verificationSetting(ctx, tx); err != nil {
			t.Fatal(err)
		}
		err = checkVerificationCoverage(ctx, p, tx, relations, c.Progress.Ranges)
		var invalid *blocked
		if wantFailure {
			if !errors.As(err, &invalid) || invalid.code != "verification_tracking_lost" {
				t.Fatal("broken range chain accepted", err)
			}
		} else if err != nil {
			t.Fatal("valid range chain rejected", err)
		}
	}
	checkCoverage(false)
	ranges, _ := verificationNames(p, 0)
	var changed int64
	var lower []byte
	if err = target.QueryRow(ctx, "SELECT id,lower_keys FROM "+ranges+" WHERE lower_keys<>'null'::jsonb LIMIT 1").Scan(&changed, &lower); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Exec(ctx, "UPDATE "+ranges+" SET lower_keys='null' WHERE id=$1", changed); err != nil {
		t.Fatal(err)
	}
	checkCoverage(true)
	if _, err = target.Exec(ctx, "UPDATE "+ranges+" SET lower_keys=$2 WHERE id=$1", changed, lower); err != nil {
		t.Fatal(err)
	}
	checkCoverage(false)
	if _, err = target.Exec(ctx, `SET session_replication_role=replica;TRUNCATE public.verification_case;RESET session_replication_role`); err != nil {
		t.Fatal(err)
	}
	if dirty, err := countDirtyVerification(ctx, p, target, relations); err != nil || dirty != c.Progress.Ranges {
		t.Fatal("replica truncate did not invalidate every range", dirty, c.Progress.Ranges, err)
	}
	if _, err = target.Exec(ctx, `ALTER TABLE public.verification_case DISABLE TRIGGER gregale_verify_rows`); err != nil {
		t.Fatal(err)
	}
	var blockedErr *blocked
	if err = checkVerificationTracking(ctx, p, target, relations, id); !errors.As(err, &blockedErr) || blockedErr.code != "verification_tracking_lost" {
		t.Fatal("disabled tracking accepted", err)
	}
	cleanupScope := p
	cleanupScope.Tables = append([]string{"different_table"}, p.Tables...)
	if err = cleanupVerification(ctx, cleanupScope, target); err != nil {
		t.Fatal(err)
	}
}
