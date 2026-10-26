package logarchive

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A quiet instance's ring can retire lines for a day long after that day's
// local history was purged. The next flush must keep what the bucket
// already holds for the day instead of replacing it with the new lines.
func TestShipper_FlushAfterPurgedHistoryKeepsTheArchivedDay(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	ts := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	writeArchiveLine(t, sh.spool, ts, 1)
	if _, _, err := sh.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(sh.cfg.SpoolRoot, "instance", "2026", "09", "log-2026-09-22") + archiveGzipSuffix
	if err := os.Remove(history); err != nil {
		t.Fatalf("drop local history: %v", err)
	}

	writeArchiveLine(t, sh.spool, ts.Add(time.Hour), 2)
	if _, _, err := sh.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := archivedSequences(t, fake.objects[sh.bucketKey("instance", "2026-09-22")])
	if !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("archived day = %v, want [1 2]: the flush replaced what the bucket held", got)
	}
}

// When the bucket has nothing for the day either, the flush starts the day.
func TestShipper_FirstFlushWithoutHistoryOrObject(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	writeArchiveLine(t, sh.spool, time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC), 7)
	if _, _, err := sh.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := archivedSequences(t, fake.objects[sh.bucketKey("instance", "2026-09-23")]); !slices.Equal(got, []int64{7}) {
		t.Fatalf("archived day = %v, want [7]", got)
	}
}
