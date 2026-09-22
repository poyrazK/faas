package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Issue #562: every flush replaces the same daily object, so the replacement
// must include earlier successful flushes, including across process restarts.
func TestShipper_RepeatedFlushPreservesDailyHistory(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	ts := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for seq := int64(1); seq <= 3; seq++ {
		if _, err := sh.spool.Write("instance", seq, "stdout", ts, "line"); err != nil {
			t.Fatal(err)
		}
		if count, _, err := sh.RunOnce(context.Background()); err != nil || count != 1 {
			t.Fatalf("flush %d = %d, %v", seq, count, err)
		}
		got := archivedSequences(t, fake.objects[sh.bucketKey("instance", "2026-09-22")])
		if len(got) != int(seq) {
			t.Fatalf("flush %d retained sequences %v, want every sequence from 1 through %d", seq, got, seq)
		}
		for i, n := range got {
			if n != int64(i+1) {
				t.Fatalf("flush %d sequences = %v, want ordered history without duplicates", seq, got)
			}
		}
		if sh.spool.LocalBytes() != 0 {
			t.Fatalf("flushed bytes still consume pending capacity: %d", sh.spool.LocalBytes())
		}
		if err := sh.spool.CloseAll(); err != nil {
			t.Fatal(err)
		}
		sh.spool = NewSpool(sh.cfg.SpoolRoot, 1<<20)
	}
}

func archivedSequences(t *testing.T, body []byte) []int64 {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	d := json.NewDecoder(r)
	var seqs []int64
	for {
		var line spoolLine
		if err := d.Decode(&line); err == io.EOF {
			return seqs
		} else if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, line.Seq)
	}
}

// The documented bytes-shipped result and metric count compressed wire bytes,
// not uncompressed JSONL; repetitive logs make the distinction observable.
func TestShipper_ReportsCompressedUploadBytes(t *testing.T) {
	sh, fake, metrics := newTestShipper(t, 7)
	ts := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if _, err := sh.spool.Write("instance", 1, "stdout", ts, strings.Repeat("x", 4096)); err != nil {
		t.Fatal(err)
	}
	_, shipped, err := sh.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := int64(len(fake.objects[sh.bucketKey("instance", "2026-09-22")]))
	if shipped != want || metrics.bytes != want {
		t.Fatalf("reported/metric bytes = %d/%d, want compressed object length %d", shipped, metrics.bytes, want)
	}
}

// Spec §11 / issue #562: each instance occupies one directory below the root.
// Dot components bypass that confinement without containing a path separator.
func TestSpool_RejectsDotInstanceDirectories(t *testing.T) {
	for _, id := range []string{".", ".."} {
		t.Run(id, func(t *testing.T) {
			parent := t.TempDir()
			s := NewSpool(filepath.Join(parent, "spool"), 1<<20)
			defer func() { _ = s.CloseAll() }()
			ts := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
			if _, err := s.Write(id, 1, "stdout", ts, "must not escape"); err == nil {
				t.Error("Write accepted a dot instance directory")
			}
			if _, err := s.PrepareUpload(id, "2026-09-22"); err == nil {
				t.Error("PrepareUpload accepted a dot instance directory")
			}
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("invalid instance created files: %v", entries)
			}
		})
	}
}

func TestSpool_RejectsInvalidUploadDayComponents(t *testing.T) {
	spool := NewSpool(t.TempDir(), 1<<20)
	for _, day := range []string{"2026/09/22", "2026-13-01", "2026-02-30"} {
		if _, err := spool.PrepareUpload("instance", day); err == nil {
			t.Errorf("PrepareUpload accepted invalid day %q", day)
		}
	}
}

func TestShipper_FailedReplacementKeepsHistory(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	ts := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	writeArchiveLine(t, sh.spool, ts, 1)
	if count, _, err := sh.RunOnce(context.Background()); count != 1 || err != nil {
		t.Fatalf("first flush = %d, %v", count, err)
	}
	writeArchiveLine(t, sh.spool, ts, 2)
	fake.failNext, fake.failStatus, fake.failCode = true, http.StatusServiceUnavailable, "Unavailable"
	if count, _, err := sh.RunOnce(context.Background()); count != 0 || err != nil {
		t.Fatalf("failed flush = %d, %v", count, err)
	}
	key := sh.bucketKey("instance", "2026-09-22")
	if got := archivedSequences(t, fake.objects[key]); !slices.Equal(got, []int64{1}) {
		t.Fatalf("failed replacement changed remote history: %v", got)
	}
	sh.spool = NewSpool(sh.cfg.SpoolRoot, 1<<20)
	writeArchiveLine(t, sh.spool, ts, 3)
	if count, _, err := sh.RunOnce(context.Background()); count != 1 || err != nil {
		t.Fatalf("retry flush = %d, %v", count, err)
	}
	if got := archivedSequences(t, fake.objects[key]); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("retry history = %v, want [1 2 3]", got)
	}
}

// Exercise each on-disk commit boundary with the same state a restarted
// process sees. Replaying a remote success before its local commit is safe.
func TestShipper_RecoversInterruptedArchiveCommit(t *testing.T) {
	for _, phase := range []string{"remote accepted", "marker installed", "history installed"} {
		t.Run(phase, func(t *testing.T) {
			sh, fake, _ := newTestShipper(t, 7)
			ts := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
			key := sh.bucketKey("instance", "2026-09-22")
			writeArchiveLine(t, sh.spool, ts, 1)
			if count, _, err := sh.RunOnce(context.Background()); count != 1 || err != nil {
				t.Fatalf("initial flush = %d, %v", count, err)
			}
			writeArchiveLine(t, sh.spool, ts, 2)
			upload, err := sh.spool.PrepareUpload("instance", "2026-09-22")
			if err != nil {
				t.Fatal(err)
			}
			fragment, err := os.ReadFile(upload)
			if err != nil {
				t.Fatal(err)
			}
			var member bytes.Buffer
			gz := gzip.NewWriter(&member)
			if _, err := gz.Write(fragment); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			cumulative := append(bytes.Clone(fake.objects[key]), member.Bytes()...)
			fake.objects[key] = cumulative // S3 accepted before the simulated crash
			base := strings.TrimSuffix(upload, spoolUploadSuffix)
			pending := base + archiveGzipSuffix + ".pending"
			if err := os.WriteFile(pending, cumulative, 0o640); err != nil {
				t.Fatal(err)
			}
			if phase != "remote accepted" {
				if err := os.Rename(upload, base+spoolCommittedSuffix); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "history installed" {
				if err := os.Rename(pending, base+archiveGzipSuffix); err != nil {
					t.Fatal(err)
				}
			}
			sh.spool = NewSpool(sh.cfg.SpoolRoot, 1<<20)
			wantUploads := 0
			if phase == "remote accepted" {
				wantUploads = 1 // replay the same replacement, without duplication
			}
			if count, _, err := sh.RunOnce(context.Background()); count != wantUploads || err != nil {
				t.Fatalf("recovery = %d, %v, want %d uploads", count, err, wantUploads)
			}
			if pendingFiles := sh.spool.FilesSnapshot(); len(pendingFiles) != 0 || sh.spool.LocalBytes() != 0 {
				t.Fatalf("recovery left pending work: %v, %d bytes", pendingFiles, sh.spool.LocalBytes())
			}
			writeArchiveLine(t, sh.spool, ts, 3)
			if count, _, err := sh.RunOnce(context.Background()); count != 1 || err != nil {
				t.Fatalf("next flush = %d, %v", count, err)
			}
			if got := archivedSequences(t, fake.objects[key]); !slices.Equal(got, []int64{1, 2, 3}) {
				t.Fatalf("post-recovery history = %v, want [1 2 3]", got)
			}
		})
	}
}

func TestShipper_PurgePreservesHistoryNeededByPendingWork(t *testing.T) {
	for _, suffix := range []string{spoolPartialSuffix, spoolUploadSuffix, spoolCommittedSuffix} {
		t.Run(suffix, func(t *testing.T) {
			sh, _, _ := newTestShipper(t, 7)
			ts := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
			writeArchiveLine(t, sh.spool, ts, 1)
			if _, _, err := sh.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join(sh.cfg.SpoolRoot, "instance", "2026", "09", "log-2026-09-22")
			history := base + archiveGzipSuffix
			old := time.Now().Add(-30 * 24 * time.Hour)
			if err := os.Chtimes(history, old, old); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(base+suffix, []byte("pending"), 0o640); err != nil {
				t.Fatal(err)
			}
			if n, err := sh.PurgeOnce(context.Background()); err != nil || n != 0 {
				t.Fatalf("purge = %d, %v, want history pinned by pending work", n, err)
			}
			if _, err := os.Stat(history); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func writeArchiveLine(t *testing.T, spool *Spool, ts time.Time, seq int64) {
	t.Helper()
	if _, err := spool.Write("instance", seq, "stdout", ts, "line"); err != nil {
		t.Fatal(err)
	}
}

func TestSpool_ArchiveCommitFailureRemainsRecoverable(t *testing.T) {
	spool := NewSpool(t.TempDir(), 1<<20)
	ts := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	writeArchiveLine(t, spool, ts, 1)
	upload, err := spool.PrepareUpload("instance", "2026-09-22")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(upload, spoolUploadSuffix)
	history := base + archiveGzipSuffix
	pending := history + ".pending"
	// A directory in place of the target forces the post-S3 promotion to
	// fail. The candidate and commit marker must survive for another pass.
	if err := os.Mkdir(history, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pending, []byte("accepted gzip"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := spool.commitArchive(upload); err == nil {
		t.Fatal("commit ignored promotion failure")
	}
	if _, err := os.Stat(base + spoolCommittedSuffix); err != nil {
		t.Fatalf("failed commit lost its marker: %v", err)
	}
	if err := os.Remove(history); err != nil { // only the empty test obstruction
		t.Fatal(err)
	}
	spool = NewSpool(spool.root, 1<<20)
	if path, err := spool.PrepareUpload("instance", "2026-09-22"); err != nil || path != "" {
		t.Fatalf("recovery = %q, %v, want commit-only completion", path, err)
	}
	got, err := os.ReadFile(history)
	if err != nil || string(got) != "accepted gzip" || spool.LocalBytes() != 0 {
		t.Fatalf("recovered history = %q, err = %v, pending bytes = %d", got, err, spool.LocalBytes())
	}
}
