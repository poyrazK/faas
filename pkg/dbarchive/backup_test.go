// adr: 1027
package dbarchive

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

// baseTar builds a pg_basebackup-like tar whose backup_label starts at lsn
// in segment wal.
func baseTar(t *testing.T, lsn, wal string, start time.Time, payload string) []byte {
	t.Helper()
	label := "START WAL LOCATION: " + lsn + " (file " + wal + ")\nCHECKPOINT LOCATION: " + lsn +
		"\nBACKUP METHOD: streamed\nBACKUP FROM: primary\nSTART TIME: " + start.UTC().Format("2006-01-02 15:04:05 MST") +
		"\nLABEL: gregale\nSTART TIMELINE: 1\n"
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range map[string]string{"backup_label": label, "PG_VERSION": "17\n", "base/1/1": payload} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func history(stop time.Time) string {
	return "START WAL LOCATION: 0/2000028\nSTOP WAL LOCATION: 0/2000100\nSTOP TIME: " + stop.UTC().Format("2006-01-02 15:04:05 MST") + "\n"
}

func TestBaseBackupStoreSelectAndRestoreStream(t *testing.T) {
	store := newStore(t)
	server := &Server{Store: store, Backups: store}
	clock := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	server.now = func() time.Time { return clock }
	dial := serveServer(t, server)
	ctx := context.Background()

	first := baseTar(t, "0/2000028", "000000010000000000000002", clock.Add(-time.Minute), "first")
	id1, err := dial(Header{DatabaseID: dbA}).PutBackup(bytes.NewReader(first))
	if err != nil || id1 != "20261013T090000Z" {
		t.Fatalf("put first = %q, %v", id1, err)
	}
	// PostgreSQL archives the backup history file at pg_backup_stop.
	if err := store.PutWAL(ctx, dbA, "000000010000000000000002.00000028.backup", bytes.NewReader([]byte(history(clock))), int64(len(history(clock)))); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	second := baseTar(t, "0/5000028", "000000010000000000000005", clock.Add(-time.Minute), "second")
	if _, err := dial(Header{DatabaseID: dbA}).PutBackup(bytes.NewReader(second)); err != nil {
		t.Fatal(err)
	}
	if err := store.PutWAL(ctx, dbA, "000000010000000000000005.00000028.backup", bytes.NewReader([]byte(history(clock))), int64(len(history(clock)))); err != nil {
		t.Fatal(err)
	}
	backups, err := store.ListBackups(dbA)
	if err != nil || len(backups) != 2 || backups[0].StartWAL != "000000010000000000000002" || backups[1].Bytes != int64(len(second)) {
		t.Fatalf("catalog = %+v, %v", backups, err)
	}

	// A restore between the two backups gets the first; after both, the
	// second; before either, nothing.
	for _, tc := range []struct {
		target time.Time
		want   []byte
	}{{clock.Add(-30 * time.Minute), first}, {clock.Add(time.Minute), second}} {
		var got bytes.Buffer
		id, err := dial(Header{DatabaseID: dbB, RestoreSource: dbA}).GetBackup(tc.target, &got)
		if err != nil || !bytes.Equal(got.Bytes(), tc.want) {
			t.Fatalf("restore to %v = %q (%d bytes), %v", tc.target, id, got.Len(), err)
		}
	}
	if _, err := dial(Header{DatabaseID: dbB, RestoreSource: dbA}).GetBackup(clock.Add(-3*time.Hour), &bytes.Buffer{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("target before every backup = %v, want ErrNotFound", err)
	}
	// Without a restore source a database reads only its own backups.
	if _, err := dial(Header{DatabaseID: dbB}).GetBackup(clock.Add(time.Minute), &bytes.Buffer{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another database's backups were served: %v", err)
	}
}

func TestBaseBackupWithoutLabelIsRefused(t *testing.T) {
	store := newStore(t)
	dial := serveServer(t, &Server{Store: store, Backups: store})
	if _, err := dial(Header{DatabaseID: dbA}).PutBackup(bytes.NewReader([]byte("not a tar"))); err == nil {
		t.Fatal("a stream without backup_label was stored")
	}
	if backups, _ := store.ListBackups(dbA); len(backups) != 0 {
		t.Fatalf("catalog after a refused backup = %+v", backups)
	}
}
