package state_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
)

// adr: 818
func TestObjectConditionalURLMigrationRoundTrip(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	base := `{"method":"PUT","key":"key","expires_in":60,"size_bytes":3}`
	for _, tc := range []struct {
		request string
		valid   bool
	}{
		{base, true},
		{strings.TrimSuffix(base, "}") + `,"if_match":"\"old\""}`, true},
		{strings.TrimSuffix(base, "}") + `,"if_none_match":"*"}`, true},
		{strings.TrimSuffix(base, "}") + `,"if_match":"old","if_none_match":"*"}`, false},
		{strings.TrimSuffix(base, "}") + `,"if_match":null}`, false},
		{strings.TrimSuffix(base, "}") + `,"if_match":""}`, false},
		{strings.TrimSuffix(base, "}") + `,"if_none_match":"old"}`, false},
		{strings.TrimSuffix(base, "}") + `,"if_match":"bad\nheader"}`, false},
		{strings.TrimSuffix(base, "}") + `,"extra":true}`, false},
		{`{"method":"GET","key":"key","expires_in":60,"if_none_match":"*"}`, false},
		{`{"method":"GET","key":"key","expires_in":60,"version_id":"00000000-0000-4000-8000-000000000001"}`, true},
		{strings.TrimSuffix(base, "}") + `,"content_type":"application/octet-stream","multipart":{"upload_id":"00000000-0000-4000-8000-000000000001","part_number":1},"if_none_match":"*"}`, false},
	} {
		var valid bool
		if err := pool.QueryRow(ctx, `SELECT valid_object_conditional_url_request($1::jsonb)`, tc.request).Scan(&valid); err != nil || valid != tc.valid {
			t.Fatal("conditional authority validator", tc, valid, err)
		}
	}
	data, err := migrations.FS.ReadFile("20261008214233280_object_conditional_put_capabilities.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing conditional rollback")
	}
	for _, sql := range []string{parts[1], parts[0], parts[0]} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal("conditional authority round trip/replay", err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='object_storage_s3_credentials'::regclass AND conname='object_s3_conditional_url_request'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("conditional authority CHECK missing", count, err)
	}
}

// adr: 818
func TestObjectConditionalURLRollbackGuard(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	objectURLCapabilitySuite(t, st)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_s3_credentials WHERE url_request ? 'if_match' OR url_request ? 'if_none_match'`).Scan(&count); err != nil || count == 0 {
		t.Fatal("missing persisted conditional authority", count, err)
	}
	data, err := migrations.FS.ReadFile("20261008214233280_object_conditional_put_capabilities.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, strings.SplitN(string(data), "-- +goose Down", 2)[1])
	_ = tx.Rollback(ctx)
	if err == nil || !strings.Contains(err.Error(), "Cannot discard persisted conditional write authority") {
		t.Fatal("rollback discarded conditional authority", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='object_storage_s3_credentials'::regclass AND conname='object_s3_conditional_url_request'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected rollback removed authority CHECK", count, err)
	}
}
