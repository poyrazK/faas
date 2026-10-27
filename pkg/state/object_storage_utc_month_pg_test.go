//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectStorageOwnerIsUTCMonthly pins the UTC month boundary of object
// storage ownership: a database session in another time zone must attribute
// a company bucket to the same owner as the UTC period does. Under a +14 h
// session the old date_trunc('month', now()) began the month 14 hours early,
// so a bucket adopted this month fell back to its creator and admission could
// not find it in the company's usage.
func TestObjectStorageOwnerIsUTCMonthly(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	f := setupOffboarding(t, state.NewPgStore(pool))
	adoptObjectCompany(t, f)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context()) //nolint:errcheck
	if _, err = tx.Exec(t.Context(), `SET LOCAL TimeZone = 'Pacific/Kiritimati'`); err != nil {
		t.Fatal(err)
	}
	var session, utc string
	if err = tx.QueryRow(t.Context(), `SELECT object_storage_usage_subject($1::uuid,$2::uuid)::text,
		object_storage_usage_subject($1::uuid,$2::uuid,date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::text`,
		f.org.ID, f.owner.ID).Scan(&session, &utc); err != nil {
		t.Fatal(err)
	}
	if session != utc || utc != f.org.ID {
		t.Fatalf("owner in a +14 h session = %s, UTC owner = %s, want the company %s", session, utc, f.org.ID)
	}
}
