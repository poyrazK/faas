package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
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

// Logs/traces audit 2026-10-31 A4: a live migration keeps the instance ID,
// so the source and the destination node both ship the same day object.
// The source's final flush (its retired ring) rebuilt the day from its own
// history and wiped the destination's lines; the destination's next flush
// rebuilt it from its history and wiped the source's final lines. Every
// whole-day rebuild now starts from what the bucket holds.
func TestShipper_MigratedInstanceKeepsBothNodesLines(t *testing.T) {
	fake := newFakeS3Bucket()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	source, _ := newTestShipperOn(t, srv.URL, 7)
	dest, _ := newTestShipperOn(t, srv.URL, 7)
	at := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	key := source.bucketKey("instance", "2026-10-31")
	ship := func(sh *Shipper, seq int64, msg string, want ...string) {
		t.Helper()
		if _, err := sh.spool.Write("instance", seq, "stdout", at.Add(time.Duration(seq)*time.Minute), msg); err != nil {
			t.Fatal(err)
		}
		if _, _, err := sh.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := archivedMessages(t, fake.objects[key]); !slices.Equal(got, want) {
			t.Fatalf("after %q the day holds %q, want %q", msg, got, want)
		}
	}
	ship(source, 1, "source before migration", "source before migration")
	// The destination's ring numbers from 1 again.
	ship(dest, 1, "destination after adoption", "source before migration", "destination after adoption")
	ship(source, 2, "source retired ring", "source before migration", "destination after adoption", "source retired ring")
	ship(dest, 2, "destination later", "source before migration", "destination after adoption", "source retired ring", "destination later")
	ship(dest, 3, "destination steady", "source before migration", "destination after adoption", "source retired ring", "destination later", "destination steady")
}

// A node that wrote the day last does not download it again: the GET is
// conditional on its history's MD5 and the store answers 304.
func TestShipper_UnchangedDayIsNotDownloaded(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	at := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	for seq := int64(1); seq <= 3; seq++ {
		writeArchiveLine(t, sh.spool, at, seq)
		if _, _, err := sh.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if fake.notModified != 2 {
		t.Fatalf("conditional reads answered 304 = %d, want 2", fake.notModified)
	}
	if got := archivedSequences(t, fake.objects[sh.bucketKey("instance", "2026-10-31")]); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("archived day = %v, want [1 2 3]", got)
	}
}

// An object in the bucket that is not a readable archive must not stop the
// day from shipping: the rebuild falls back to this node's history.
func TestShipper_UnreadableDayObjectFallsBackToHistory(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	at := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	key := sh.bucketKey("instance", "2026-10-31")
	writeArchiveLine(t, sh.spool, at, 1)
	if _, _, err := sh.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.objects[key] = []byte("not a gzip stream")
	writeArchiveLine(t, sh.spool, at, 2)
	if shipped, _, err := sh.RunOnce(context.Background()); err != nil || shipped != 1 {
		t.Fatalf("flush = %d, %v", shipped, err)
	}
	if got := archivedSequences(t, fake.objects[key]); !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("archived day = %v, want [1 2]", got)
	}
}

// A replacement the store accepted while this node saw an error is this
// node's own object, not another writer's: the retry must not repeat its
// fragment, even when newer lines were merged into the same upload.
func TestShipper_RetryAfterAcceptedFailedPutDoesNotDuplicate(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	at := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	key := sh.bucketKey("instance", "2026-10-31")
	writeArchiveLine(t, sh.spool, at, 1)
	if _, _, err := sh.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeArchiveLine(t, sh.spool, at, 2)
	fake.acceptThenFail = true
	if shipped, _, err := sh.RunOnce(context.Background()); err != nil || shipped != 0 {
		t.Fatalf("failed flush = %d, %v", shipped, err)
	}
	if got := archivedSequences(t, fake.objects[key]); !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("store holds %v, want the accepted [1 2]", got)
	}
	writeArchiveLine(t, sh.spool, at, 3)
	if shipped, _, err := sh.RunOnce(context.Background()); err != nil || shipped != 1 {
		t.Fatalf("retry = %d, %v", shipped, err)
	}
	if got := archivedSequences(t, fake.objects[key]); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("archived day = %v, want [1 2 3] without a repeated fragment", got)
	}
}

func archivedMessages(t *testing.T, body []byte) []string {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var msgs []string
	for d := json.NewDecoder(r); ; {
		var line spoolLine
		if err := d.Decode(&line); err == io.EOF {
			return msgs
		} else if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, line.Line)
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
