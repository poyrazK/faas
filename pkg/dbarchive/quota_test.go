// adr: 1027 — a guest cannot fill the node's archive disk: vmmd stamps a
// quota on its streams and dbbackupd refuses WAL and backups past it.
package dbarchive

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveQuotaRefusesWALAndBackupsPastIt(t *testing.T) {
	local := newStore(t)
	tiered := &TieredStore{LocalStore: local}
	open := serveServer(t, &Server{Store: tiered, Backups: tiered, Retention: tiered})
	wal := bytes.Repeat([]byte("w"), 4096)

	guest := open(Header{DatabaseID: dbA, QuotaBytes: 10_000})
	if err := guest.PutWAL(segName(1), bytes.NewReader(wal), int64(len(wal))); err != nil {
		t.Fatal(err)
	}
	if err := guest.PutWAL(segName(2), bytes.NewReader(wal), int64(len(wal))); err != nil {
		t.Fatal(err)
	}
	// 8 KiB archived; another 4 KiB would pass the 10,000-byte quota.
	if err := guest.PutWAL(segName(3), bytes.NewReader(wal), int64(len(wal))); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("WAL past the quota = %v, want ErrQuotaExceeded", err)
	}
	if _, err := os.Stat(filepath.Join(local.dir, dbA, "wal", segName(3))); !os.IsNotExist(err) {
		t.Fatalf("a refused segment was stored: %v", err)
	}

	// A base backup stream past the quota is cut off and leaves nothing.
	big := strings.NewReader(strings.Repeat("b", 64<<10))
	if _, err := open(Header{DatabaseID: dbA, QuotaBytes: 20_000}).PutBackup(big); err == nil {
		t.Fatal("a backup past the quota was stored")
	}
	entries, _ := os.ReadDir(filepath.Join(local.dir, dbA, "base"))
	if len(entries) != 0 {
		t.Fatalf("a refused backup left %d files", len(entries))
	}

	// Another database's quota is its own; no quota means none.
	if err := open(Header{DatabaseID: dbB, QuotaBytes: 10_000}).PutWAL(segName(1), bytes.NewReader(wal), int64(len(wal))); err != nil {
		t.Fatalf("another database: %v", err)
	}
	if err := open(Header{DatabaseID: dbA}).PutWAL(segName(3), bytes.NewReader(wal), int64(len(wal))); err != nil {
		t.Fatalf("no quota: %v", err)
	}
	if err := (Header{DatabaseID: dbA, QuotaBytes: -1}).Validate(); err == nil {
		t.Fatal("a negative quota validated")
	}
	if err := (Header{DatabaseID: dbA, Admin: true, QuotaBytes: 1}).Validate(); err == nil {
		t.Fatal("an admin header with a quota validated")
	}
}
