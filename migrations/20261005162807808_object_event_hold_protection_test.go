package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 608
func TestObjectEventHoldProtectionMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if e := db.MigrateUp(t.Context(), pool); e != nil {
		t.Fatal(e)
	}
	data, e := migrations.FS.ReadFile("20261005162807808_object_event_hold_protection.sql")
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.SplitN(string(data), "-- +goose Down", 2)
	if _, e = pool.Exec(t.Context(), parts[1]); e != nil {
		t.Fatal("empty rollback", e)
	}
	for range 2 {
		if _, e = pool.Exec(t.Context(), parts[0]); e != nil {
			t.Fatal("reapply", e)
		}
	}
	for _, tc := range []struct {
		doc         string
		write, want bool
	}{
		{`{}`, true, true},
		{`{"mode":"COMPLIANCE","event_hold":"OFF"}`, true, true},
		{`{"mode":"COMPLIANCE","event_hold":"OFF"}`, false, false},
		{`{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"years":100}}`, true, true},
		{`{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":36501}}`, true, false},
		{`{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":1.5}}`, true, false},
		{`{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":0}}`, true, false},
		{`{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":1,"years":1}}`, true, false},
		{`{"mode":"COMPLIANCE","event_hold":"OFF","event_hold_duration":{"days":1},"retain_until_date":"2027-10-05T00:00:00Z"}`, false, true},
		{`{"mode":"COMPLIANCE","event_hold":"OFF","event_hold_duration":{"days":1},"retain_until_date":"2027-10-05T00:00:00Z"}`, true, false},
		{`{"mode":"COMPLIANCE","retain_until_date":"infinity"}`, true, false},
		{`{"mode":"COMPLIANCE","retain_until_date":"invalid"}`, true, false},
		{`{"mode":"COMPLIANCE","retain_until_date":null}`, true, false},
		{`{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":1},"unknown":1}`, true, false},
	} {
		var got bool
		if e = pool.QueryRow(t.Context(), `SELECT object_event_hold_retention_valid($1::jsonb,$2)`, tc.doc, tc.write).Scan(&got); e != nil || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
}
