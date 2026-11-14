package datapipeline

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestVerificationChecksOversizedValuesBeforeJSONEncoding(t *testing.T) {
	for _, tc := range []struct {
		name, kind, value string
		limited           bool
	}{
		{"small bytea", "bytea", `convert_to(repeat('x',1024),'UTF8')`, false},
		{"hexadecimal expansion", "bytea", `convert_to(repeat('x',9*1024*1024),'UTF8')`, true},
		{"JSONB string representation bound", "bytea", `convert_to(repeat('x',129*1024*1024),'UTF8')`, true},
		{"oversized text", "text", `repeat('x',17*1024*1024)`, true},
		{"JSON escaping expansion", "text", `repeat(chr(1),3*1024*1024)`, true},
		{"nullable text", "text", `NULL`, false},
		{"JSON null value", "json", `'null'::json`, false},
		{"small compact numeric JSONB", "jsonb", `'[1e1000, null]'::jsonb`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := pgtest.OpenDatabase(t)
			ctx := t.Context()
			if _, err := pool.Exec(ctx, "CREATE TABLE public.sized_row(id int PRIMARY KEY,value "+tc.kind+");INSERT INTO public.sized_row VALUES(1,"+tc.value+")"); err != nil {
				t.Fatal(err)
			}
			r, table, err := inspectSourceTable(ctx, pool, "sized_row")
			if err != nil || !table.Supported {
				t.Fatal("unsupported size fixture", table, err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if err = verificationSetting(ctx, tx); err != nil {
				t.Fatal(err)
			}
			result, err := scanVerificationRows(ctx, tx, r, nil, nil, true, true)
			if err != nil || result.Limited != tc.limited {
				t.Fatal("size handling failed", result.Limited, err)
			}
			if tc.limited {
				if result.LimitReason != "verification_row_limit" || len(result.Data) != 0 {
					t.Fatal("oversized row was captured or misclassified", result.LimitReason)
				}
			} else if result.Rows != 1 || len(result.Data) != 1 || result.Hash == "" {
				t.Fatal("valid row was not captured", result)
			}
			var active int
			if err = tx.QueryRow(ctx, "SELECT 1").Scan(&active); err != nil {
				t.Fatal("size check aborted the parent transaction", err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal("size result could not commit", err)
			}
		})
	}
}

func TestVerificationReadTimeoutDoesNotBecomeARowLimit(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE public.locked_scan(id int PRIMARY KEY,value text);INSERT INTO public.locked_scan VALUES(1,'normal')`); err != nil {
		t.Fatal(err)
	}
	r, _, err := inspectSourceTable(ctx, pool, "locked_scan")
	if err != nil {
		t.Fatal(err)
	}
	locker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Rollback(ctx) }()
	if _, err = locker.Exec(ctx, `LOCK TABLE public.locked_scan IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		t.Fatal(err)
	}
	result, err := scanVerificationRange(ctx, tx, r, nil, nil, true)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "55P03" || result.Limited {
		t.Fatal("lock timeout was masked as a data limit", result.Limited, err)
	}
	var active int
	if err = tx.QueryRow(ctx, "SELECT 1").Scan(&active); err != nil {
		t.Fatal("failed scan aborted its parent", err)
	}
	if err = locker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	result, err = scanVerificationRange(ctx, tx, r, nil, nil, true)
	if err != nil || result.Limited || result.Rows != 1 || result.LimitReason != "" || result.Bytes > api.DataPipelineVerificationBytesMax {
		t.Fatal("scan could not resume after its lock cleared", result, err)
	}
}
