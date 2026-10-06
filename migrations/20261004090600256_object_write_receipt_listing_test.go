package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectWriteReceiptListingMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261004090600256_object_write_receipt_listing.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, `CREATE TABLE object_upload_completions(id uuid PRIMARY KEY,bucket_id uuid,created_at timestamptz DEFAULT now(),status text,write_phase text); INSERT INTO object_upload_completions VALUES('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002',now(),'pending','dispatched')`); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[0], parts[1], parts[0]} {
		if _, err = pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_upload_completions WHERE status='pending' AND write_phase='dispatched'`).Scan(&count); err != nil || count != 1 {
			t.Fatal("listing migration changed receipt", count, err)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('object_upload_completions_bucket_receipts_idx','object_upload_completions_bucket_receipt_status_idx') AND indexdef LIKE '%untracked%'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("missing bounded listing indexes", count, err)
	}
}
