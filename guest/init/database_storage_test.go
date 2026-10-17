// adr: 1027 — a primary short of volume space turns read-only before
// PostgreSQL would PANIC, and writable again with hysteresis.
package main

import (
	"slices"
	"strings"
	"testing"
)

func TestStorageThresholds(t *testing.T) {
	const mib, gib = 1 << 20, 1 << 30
	for total, want := range map[uint64]uint64{1 * gib: 128 * mib, 10 * gib: 512 * mib, 40 * gib: 2 * gib, 500 * gib: 2 * gib} {
		if got := storageReadOnlyBelow(total); got != want {
			t.Errorf("read-only below for %d = %d, want %d", total, got, want)
		}
		if storageWritableAbove(total) != 2*want {
			t.Errorf("writable above for %d = %d", total, storageWritableAbove(total))
		}
		// Laying the ballast at the writable level never crosses back.
		if storageWritableAbove(total)-storageBallastBytes(total) < storageReadOnlyBelow(total) {
			t.Errorf("ballast %d flips a %d volume back to read-only", storageBallastBytes(total), total)
		}
	}
}

func TestNextStorageReadOnlyHasHysteresis(t *testing.T) {
	const gib = 1 << 30
	below, above := storageReadOnlyBelow(gib), storageWritableAbove(gib)
	for _, c := range []struct {
		readOnly bool
		free     uint64
		want     bool
	}{
		{false, above, false},
		{false, below, false},
		{false, below - 1, true},
		{true, below - 1, true},
		{true, above - 1, true},
		{true, above, false},
	} {
		if got := nextStorageReadOnly(c.readOnly, c.free, gib); got != c.want {
			t.Errorf("read-only %v with %d free = %v, want %v", c.readOnly, c.free, got, c.want)
		}
	}
}

func TestStorageModeStatements(t *testing.T) {
	if got := storageModeStatements(true); !slices.Equal(got, []string{"ALTER SYSTEM SET default_transaction_read_only = on", "SELECT pg_catalog.pg_reload_conf()"}) {
		t.Fatalf("read-only = %q", got)
	}
	if got := storageModeStatements(false); got[0] != "ALTER SYSTEM RESET default_transaction_read_only" {
		t.Fatalf("writable = %q", got)
	}
	if !strings.Contains(databaseAdminOptions, "default_transaction_read_only=off") || !strings.HasPrefix(databaseAdminOptions, "PGOPTIONS=") {
		t.Fatalf("admin sessions are not exempt: %q", databaseAdminOptions)
	}
}
