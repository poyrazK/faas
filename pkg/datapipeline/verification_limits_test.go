package datapipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func largeVerificationKeySQL(name string, columns int) string {
	var definitions, names, first, second []string
	for i := range columns {
		name := ident(fmt.Sprintf("k%d", i))
		definitions = append(definitions, name+" numeric NOT NULL")
		names = append(names, name)
		first = append(first, "'1e131071'::numeric")
		value := "'1e131071'::numeric"
		if i == 0 {
			value = "'2e131071'::numeric"
		}
		second = append(second, value)
	}
	return "CREATE TABLE " + tableIdent(name) + "(" + strings.Join(definitions, ",") + ",PRIMARY KEY(" + strings.Join(names, ",") + "));" +
		"INSERT INTO " + tableIdent(name) + " VALUES(" + strings.Join(first, ",") + "),(" + strings.Join(second, ",") + ")"
}

func TestVerificationBoundsLargeNumericKeysAndRetiresTracking(t *testing.T) {
	for _, columns := range []int{1, 32} {
		t.Run(fmt.Sprint(columns), func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			ctx := t.Context()
			p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Revision: 1, Tables: []string{"verification_large_key"}}}
			if _, err := pool.Exec(ctx, largeVerificationKeySQL(p.Tables[0], columns)+";COMMENT ON TABLE "+tableIdent(p.Tables[0])+" IS "+literal(marker(p))+";CREATE SCHEMA gregale_pipeline"); err != nil {
				t.Fatal(err)
			}
			r, discovered, err := inspectSourceTable(ctx, pool, p.Tables[0])
			if err != nil || !discovered.Supported {
				t.Fatal("valid numeric primary key was rejected", discovered, err)
			}
			a, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Release()
			target, relations := a.Conn(), []relation{r}
			intent, spec := verificationID(p, relations)
			storage, err := verificationSourceStorage(ctx, pool, relations)
			if err != nil {
				t.Fatal(err)
			}
			c, err := startVerification(ctx, p, target, relations, physicalVerificationID(intent, storage), intent, spec, storage, 0, func(context.Context) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
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
			moved, err := compareLockedVerificationRange(ctx, p, src, dst, r, c, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if columns == 1 {
				if !moved || c.Progress.Rows != 2 || c.Table != 1 || c.Progress.State != "running" {
					t.Fatal("key below the budget did not compare successfully", moved, c.Progress)
				}
			} else if moved || c.Progress.State != "limited" || c.Progress.Reason != "verification_key_limit" || len(c.Cursor) != 0 || c.Progress.Ranges != 0 {
				t.Fatal("oversized key did not stop before checkpointing", moved, c.Progress)
			}
			if err = saveVerification(ctx, dst, p, c); err != nil {
				t.Fatal("bounded result could not checkpoint", err)
			}
			if err = dst.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			// This fixture scans the same physical table from both connections.
			// Release its source read lock before retiring target triggers.
			if err = src.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if columns == 32 {
				if err = retireVerification(ctx, p, target, relations, c, func(context.Context) error { return nil }); err != nil {
					t.Fatal(err)
				}
				assertVerificationHelpersRetired(t, p, target, r)
				saved, err := loadVerification(ctx, target, p)
				if err != nil || saved == nil || saved.Progress.State != "limited" || saved.Progress.Phase != "stopped" || len(saved.Cursor) != 0 {
					t.Fatal("stopped limit result did not survive reload", err)
				}
			}
		})
	}
}

func assertVerificationHelpersRetired(t *testing.T, p Pipeline, target receiptSQL, r relation) {
	t.Helper()
	ranges, fn := verificationNames(p, 0)
	var retired bool
	err := target.QueryRow(t.Context(), `SELECT to_regclass($1) IS NULL AND to_regprocedure($2) IS NULL
 AND NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass($3) AND tgname IN ('gregale_verify_rows','gregale_verify_truncate'))`, ranges, fn+"()", tableIdent(r.Name)).Scan(&retired)
	if err != nil || !retired {
		t.Fatal("verification helpers were retained", retired, err)
	}
}
