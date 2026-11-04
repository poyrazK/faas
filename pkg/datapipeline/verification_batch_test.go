package datapipeline

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestVerificationLargeRowsCommitBoundedRangeProgress(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE public.review_large(id int PRIMARY KEY,value text);INSERT INTO public.review_large SELECT i,repeat('x',1024*1024) FROM generate_series(1,2002)i;CREATE SCHEMA gregale_pipeline`); err != nil {
		t.Fatal(err)
	}
	r, table, err := inspectSourceTable(ctx, pool, "review_large")
	if err != nil || !table.Supported {
		t.Fatal(table, err)
	}
	p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Revision: 1, Generation: 1, Tables: []string{r.Name}}}
	if _, err = pool.Exec(ctx, "COMMENT ON TABLE "+tableIdent(r.Name)+" IS "+literal(marker(p))); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	intent, spec := verificationID(p, []relation{r})
	storage, err := verificationSourceStorage(ctx, pool, []relation{r})
	if err != nil {
		t.Fatal(err)
	}
	c, err := startVerification(ctx, p, conn.Conn(), []relation{r}, physicalVerificationID(intent, storage), intent, spec, storage, 0, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		source, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = source.Rollback(ctx) }()
		if err = verificationSetting(ctx, source); err != nil {
			t.Fatal(err)
		}
		target, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = target.Rollback(ctx) }()
		if err = verificationSetting(ctx, target); err != nil {
			t.Fatal(err)
		}
		moved, err := compareLockedVerificationRange(ctx, p, source, target, r, c, 0, nil)
		if err != nil || !moved || c.Progress.State != "running" {
			t.Fatal("valid large rows did not advance", moved, err, c.Progress)
		}
		if err = saveVerification(ctx, target, p, c); err != nil {
			t.Fatal(err)
		}
		var held int
		if err = source.QueryRow(ctx, `SELECT 1`).Scan(&held); err != nil {
			t.Fatal("bounded range left source transaction aborted", err)
		}
		if err = target.Commit(ctx); err != nil {
			t.Fatal("bounded progress did not commit", err)
		}
		_ = source.Rollback(ctx)
		if c.Progress.Ranges != int64(attempt+1) || c.Progress.Rows != int64(3*(attempt+1)) || c.Table != 0 {
			t.Fatal("range skipped or restarted large rows", c.Progress)
		}
	}
	saved, err := loadVerification(ctx, conn.Conn(), p)
	if err != nil || saved == nil || saved.Progress.Rows != 6 || len(saved.Cursor) != 1 || saved.Cursor[0] != "6" {
		t.Fatal("range progress was not durable", saved, err)
	}
}

func TestVerificationByteBudgetCannotHideTrailingRows(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE public.review_head(id int PRIMARY KEY,value text);INSERT INTO public.review_head VALUES(1,repeat('x',8*1024*1024));CREATE TABLE public.review_extra(LIKE public.review_head INCLUDING ALL);INSERT INTO public.review_extra SELECT * FROM public.review_head;INSERT INTO public.review_extra VALUES(2,'extra')`); err != nil {
		t.Fatal(err)
	}
	r, _, err := inspectSourceTable(ctx, pool, "review_head")
	if err != nil {
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
	head, err := scanVerificationRange(ctx, tx, r, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	r.Name = "review_extra"
	extra, err := scanVerificationRange(ctx, tx, r, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if head.Rows != 1 || head.Bytes <= api.DataPipelineVerificationChunkBytes || head.More || !extra.More || head.Hash != extra.Hash || extra.Rows != head.Rows {
		t.Fatal("byte bound hid a trailing mismatched row", head, extra)
	}
	var held int
	if err = tx.QueryRow(ctx, `SELECT 1`).Scan(&held); err != nil {
		t.Fatal("large allowed row aborted transaction", err)
	}
}
