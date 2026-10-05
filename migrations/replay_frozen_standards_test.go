//go:build !no_pg

package migrations_test

// adr: 592

import (
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func newMigrationReplayPool(t *testing.T, versions []int64) *pgxpool.Pool {
	t.Helper()
	frozen, err := migrations.ApplicationStandardRecoverySources()
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range versions {
		if _, ok := frozen[version]; ok {
			// Recovery verifies the complete public schema of a private database.
			t.Setenv(pgtest.UseTemplateDatabase, "1")
			return pgtest.OpenMigrated(t)
		}
	}
	return pgtest.Open(t)
}

// Only the exact filename/content manifest is eligible. New SQL never gains
// an exemption by its name, timestamp, or application-standard prefix.
func recoverFrozenStandardsForReplay(t *testing.T, pool *pgxpool.Pool, versions []int64) {
	t.Helper()
	frozen, err := migrations.ApplicationStandardRecoverySources()
	if err != nil {
		t.Fatal(err)
	}
	var expected []int64
	for _, version := range versions {
		if _, ok := frozen[version]; ok {
			expected = append(expected, version)
		}
	}
	if len(expected) == 0 {
		return
	}
	plan, err := db.PreviewApplicationStandardLedgerRecovery(t.Context(), pool)
	if err != nil {
		t.Fatalf("frozen SQL requires verified local PostgreSQL 16 recovery: %v", err)
	}
	var actual []int64
	for _, source := range plan.Repair {
		actual = append(actual, source.Version)
	}
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("reviewed repair=%v, expected exact frozen diff=%v", actual, expected)
	}
	receipt, err := db.ApplyApplicationStandardLedgerRecovery(t.Context(), pool, plan.ApprovalHash)
	if err != nil || !slices.Equal(receipt.RepairedVersions, expected) {
		t.Fatalf("verified ledger recovery: receipt=%+v err=%v", receipt, err)
	}
}
