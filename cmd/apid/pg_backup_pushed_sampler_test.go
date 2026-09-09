package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPgBackupPushedSamplerReadsVerifiedState(t *testing.T) {
	root := t.TempDir()
	swapBackupStateRoot(t, root)
	now := time.Now().UTC().Truncate(time.Second)
	writeState(t, root, "basebackup-push-success", now.Add(-5*time.Minute).Format(time.RFC3339)+"\n")
	writeState(t, root, "wal-prune-success", now.Add(-15*time.Minute).Format(time.RFC3339)+"\n")
	writeState(t, root, "wal-archive-stats", "bytes=16777216\noldest_timestamp_seconds="+
		formatUnix(now.Add(-2*time.Hour))+"\nnewest_timestamp_seconds="+
		formatUnix(now.Add(-30*time.Second))+"\n")

	ops := wire.NewOpsMetrics("apid_pg_backup_test")
	newPgBackupPushedSampler(ops, slog.New(slog.NewTextHandler(io.Discard, nil))).tick()

	assertNear(t, testutil.ToFloat64(ops.PgBackupLastPushed()), float64(now.Add(-5*time.Minute).Unix()), 1)
	assertNear(t, testutil.ToFloat64(ops.PgWalPruneLastSuccessful()), float64(now.Add(-15*time.Minute).Unix()), 1)
	assertNear(t, testutil.ToFloat64(ops.PgWalArchiveBytes()), 16777216, 0)
	assertNear(t, testutil.ToFloat64(ops.PgWalArchiveOldestAge()), 2*time.Hour.Seconds(), 2)
	assertNear(t, testutil.ToFloat64(ops.PgWalArchiveNewestAge()), 30, 2)
}

func TestPgBackupPushedSamplerMissingStateEmitsZero(t *testing.T) {
	swapBackupStateRoot(t, t.TempDir())
	ops := wire.NewOpsMetrics("apid_pg_backup_empty_test")
	newPgBackupPushedSampler(ops, slog.New(slog.NewTextHandler(io.Discard, nil))).tick()
	for name, gauge := range map[string]prometheus.Gauge{
		"backup": ops.PgBackupLastPushed(),
		"bytes":  ops.PgWalArchiveBytes(),
		"oldest": ops.PgWalArchiveOldestAge(),
		"newest": ops.PgWalArchiveNewestAge(),
		"prune":  ops.PgWalPruneLastSuccessful(),
	} {
		if got := testutil.ToFloat64(gauge); got != 0 {
			t.Fatalf("%s gauge = %v, want 0", name, got)
		}
	}
}

func TestPgBackupPushedSamplerNilOpsStops(t *testing.T) {
	s := newPgBackupPushedSampler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { s.run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run did not stop with nil metrics")
	}
}

func swapBackupStateRoot(t *testing.T, root string) {
	t.Helper()
	previous := pgBackupStateRoot
	pgBackupStateRoot = root
	t.Cleanup(func() { pgBackupStateRoot = previous })
}

func writeState(t *testing.T, root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func formatUnix(stamp time.Time) string {
	return strconv.FormatInt(stamp.Unix(), 10)
}

func assertNear(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if got < want-tolerance || got > want+tolerance {
		t.Fatalf("got %v, want %v +/- %v", got, want, tolerance)
	}
}
