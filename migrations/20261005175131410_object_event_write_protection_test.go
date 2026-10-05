package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 595
func TestObjectEventWriteProtectionMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	data, err := migrations.FS.ReadFile("20261005175131410_object_event_write_protection.sql")
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
	for _, tc := range []struct {
		requested string
		want      bool
	}{
		{`{"retention":{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":30}}}`, true},
		{`{"retention":{"mode":"GOVERNANCE","event_hold":"ON","event_hold_duration":{"years":1}}}`, true},
		{`{"retention":{"mode":"COMPLIANCE","event_hold":"OFF","retain_until_date":"2027-01-01T00:00:00Z"}}`, true},
		{`{"retention":{"mode":"COMPLIANCE","event_hold":"OFF"}}`, false},
		{`{"retention":{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":30,"years":1}}}`, false},
		{`{"retention":{"mode":"COMPLIANCE","event_hold":"ON","event_hold_duration":{"days":-1}}}`, false},
		{`{"retention":{"mode":"COMPLIANCE","event_hold":"OFF","event_hold_duration":{"days":1},"retain_until_date":"2027-01-01T00:00:00Z"}}`, false},
		{`null`, false},
	} {
		var got bool
		if err = pool.QueryRow(t.Context(), `SELECT valid_object_event_write_protection(jsonb_build_object('enabled',true,'captured_at','2026-01-01T00:00:00Z','requested',$1::jsonb))`, tc.requested).Scan(&got); err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
		if err = pool.QueryRow(t.Context(), `SELECT valid_object_event_protected_url_request(jsonb_build_object('method','PUT','key','key','size_bytes',3,'expires_in',60,'protection',$1::jsonb))`, tc.requested).Scan(&got); err != nil || got != tc.want {
			t.Fatal("URL", tc, got, err)
		}
	}
}
