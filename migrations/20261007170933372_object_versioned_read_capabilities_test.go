package migrations_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 678
func TestObjectVersionedReadCapabilityMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	data, err := migrations.FS.ReadFile("20261007170933372_object_versioned_read_capabilities.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "-- +goose Down", 2)
	if _, err = pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal("empty rollback", err)
	}
	for range 2 {
		if _, err = pool.Exec(t.Context(), parts[0]); err != nil {
			t.Fatal("replay", err)
		}
	}
	id := uuid.NewString()
	for _, tc := range []struct {
		request string
		want    bool
	}{
		{`{"method":"GET","key":"key","expires_in":60,"version_id":"` + id + `"}`, true},
		{`{"method":"HEAD","key":"key","expires_in":60,"version_id":"` + id + `"}`, true},
		{`{"method":"GET","key":"key","expires_in":60}`, true},
		{`{"method":"PUT","key":"key","size_bytes":3,"expires_in":60}`, true},
		{`{"method":"PUT","key":"key","size_bytes":3,"expires_in":60,"version_id":"` + id + `"}`, false},
		{`{"method":"GET","key":"key","expires_in":60,"version_id":"null"}`, false},
		{`{"method":"GET","key":"key","expires_in":60,"version_id":null}`, false},
		{`{"method":"GET","key":"key","expires_in":60,"version_id":123}`, false},
		{`{"method":"GET","key":"key","expires_in":60,"version_id":"native"}`, false},
		{`{"method":"GET","key":"key","expires_in":60,"version_id":"` + id + `","ignored":true}`, false},
		{`{"method":"GET","key":"key","expires_in":60,"version_id":"` + id + `","size_bytes":3}`, false},
	} {
		var got bool
		if err = pool.QueryRow(t.Context(), `SELECT valid_object_versioned_url_request($1::jsonb)`, tc.request).Scan(&got); err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	var count int
	if err = pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_constraint WHERE conrelid='object_storage_s3_credentials'::regclass AND conname='object_s3_versioned_url_request'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("constraint missing", count, err)
	}
}
