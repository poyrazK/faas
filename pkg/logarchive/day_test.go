// adr: 20261028110000 — part objects: each flush is its own object, read
// back after the whole-day object in key order.

package logarchive

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func gzipLines(t *testing.T, lines ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	for _, line := range lines {
		if _, err := gz.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// readDay returns the archived day's lines, or OpenDay's error.
func readDay(t *testing.T, s3 *S3Client, instance, day string) ([]string, error) {
	t.Helper()
	body, err := s3.OpenDay(context.Background(), instance, day)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	gz, err := gzip.NewReader(body)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	var lines []string
	scanner := bufio.NewScanner(gz)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return lines, nil
}

func newDayTestClient(t *testing.T, handler http.Handler) *S3Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	s3, err := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	if err != nil {
		t.Fatal(err)
	}
	return s3
}

func TestOpenDayReadsTheWholeDayObjectThenPartsInKeyOrder(t *testing.T) {
	const instance, day = "inst-a", "2026-10-28"
	for _, tc := range []struct {
		name    string
		objects map[string][]byte
		want    []string
	}{
		{
			name:    "whole-day object only",
			objects: map[string][]byte{DayKey(instance, day): gzipLines(t, "a", "b")},
			want:    []string{"a", "b"},
		},
		{
			name: "parts only",
			objects: map[string][]byte{
				DayPartPrefix(instance, day) + "0000000000000000002-bb.jsonl.gz": gzipLines(t, "c"),
				DayPartPrefix(instance, day) + "0000000000000000001-aa.jsonl.gz": gzipLines(t, "a", "b"),
			},
			want: []string{"a", "b", "c"},
		},
		{
			name: "whole day written before the switch, then parts",
			objects: map[string][]byte{
				DayKey(instance, day): gzipLines(t, "a"),
				DayPartPrefix(instance, day) + "0000000000000000001-aa.jsonl.gz": gzipLines(t, "b"),
				DayPartPrefix(instance, day) + "0000000000000000002-bb.jsonl.gz": gzipLines(t, "c"),
				// Another day and another instance stay out.
				DayPartPrefix(instance, "2026-10-29") + "0000000000000000001-cc.jsonl.gz": gzipLines(t, "other day"),
				DayPartPrefix("inst-b", day) + "0000000000000000001-dd.jsonl.gz":          gzipLines(t, "other instance"),
			},
			want: []string{"a", "b", "c"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeS3Bucket()
			fake.objects = tc.objects
			got, err := readDay(t, newDayTestClient(t, fake.handler()), instance, day)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("lines = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenDayWithNothingArchivedIsNotFound(t *testing.T) {
	_, err := readDay(t, newDayTestClient(t, newFakeS3Bucket().handler()), "inst-a", "2026-10-28")
	var permanent *Permanent
	if !errors.As(err, &permanent) || permanent.StatusCode != http.StatusNotFound {
		t.Fatalf("err = %v, want a 404 Permanent", err)
	}
}

// A part the lifecycle rule deletes between the listing and the read is
// skipped, not reported as a broken day.
func TestOpenDaySkipsAPartThatExpiresWhileReading(t *testing.T) {
	const instance, day = "inst-a", "2026-10-28"
	gone := DayPartPrefix(instance, day) + "0000000000000000001-aa.jsonl.gz"
	kept := DayPartPrefix(instance, day) + "0000000000000000002-bb.jsonl.gz"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Has("prefix"):
			_, _ = io.WriteString(w, "<ListBucketResult><Contents><Key>"+gone+"</Key></Contents><Contents><Key>"+kept+"</Key></Contents></ListBucketResult>")
		case strings.HasSuffix(r.URL.Path, kept):
			_, _ = w.Write(gzipLines(t, "kept"))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code><Message>gone</Message></Error>")
		}
	})
	got, err := readDay(t, newDayTestClient(t, handler), instance, day)
	if err != nil || !slices.Equal(got, []string{"kept"}) {
		t.Fatalf("lines = %q, err = %v; want only the remaining part", got, err)
	}
}

// With part objects each flush uploads only its own lines, the spool keeps no
// shipped history, and the day reads back whole and in order.
func TestShipperPartObjectsUploadEachFlushOnce(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	sh.cfg.PartObjects = true
	ts := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	day := ts.Format("2006-01-02")
	flush := func(msg string) {
		t.Helper()
		if _, err := sh.spool.Write("inst-a", 1, "stdout", ts, msg); err != nil {
			t.Fatal(err)
		}
		if err := sh.spool.CloseAll(); err != nil {
			t.Fatal(err)
		}
		if n, _, err := sh.RunOnce(context.Background()); err != nil || n != 1 {
			t.Fatalf("RunOnce = %d, %v; want one part", n, err)
		}
	}
	flush("first")
	time.Sleep(2 * time.Millisecond) // distinct seal times
	flush("second")

	if _, whole := fake.objects[DayKey("inst-a", day)]; whole {
		t.Fatal("part mode wrote the whole-day object")
	}
	var parts []string
	for key, body := range fake.objects {
		parts = append(parts, key)
		if n := strings.Count(gunzip(t, body), "\n"); n != 1 {
			t.Errorf("part %s holds %d lines, want only its own flush", key, n)
		}
	}
	if len(parts) != 2 {
		t.Fatalf("bucket = %v, want two parts", parts)
	}
	got, err := readDay(t, sh.s3, "inst-a", day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(got[0], "first") || !strings.Contains(got[1], "second") {
		t.Fatalf("day = %q, want first then second", got)
	}
	_ = filepath.WalkDir(sh.spool.root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("spool still holds %s after its part was uploaded", path)
		}
		return nil
	})
	if sh.spool.LocalBytes() != 0 {
		t.Errorf("spool size = %d, want 0", sh.spool.LocalBytes())
	}
}

// A crash after the PUT but before the shipper drops the upload resends the
// same bytes under the same key: the day has no duplicate lines.
func TestShipperPartRetryAfterCrashRewritesTheSamePart(t *testing.T) {
	sh, fake, _ := newTestShipper(t, 7)
	sh.cfg.PartObjects = true
	ts := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if _, err := sh.spool.Write("inst-a", 1, "stdout", ts, "only once"); err != nil {
		t.Fatal(err)
	}
	if err := sh.spool.CloseAll(); err != nil {
		t.Fatal(err)
	}
	upload, err := sh.spool.PreparePartUpload("inst-a", ts.Format("2006-01-02"))
	if err != nil || upload == "" {
		t.Fatalf("PreparePartUpload = %q, %v", upload, err)
	}
	saved, err := os.ReadFile(upload)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(upload)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sh.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Put the upload back as the crash left it, then retry.
	if err := os.WriteFile(upload, saved, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(upload, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sh.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.objects) != 1 {
		t.Fatalf("bucket holds %d objects, want the one part rewritten", len(fake.objects))
	}
}

// PreparePartUpload never grows a pending upload: new lines wait for the next
// flush, so a retried part keeps its key.
func TestPreparePartUploadLeavesNewLinesForTheNextFlush(t *testing.T) {
	spool := NewSpool(t.TempDir(), 1<<20)
	ts := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	day := ts.Format("2006-01-02")
	write := func(msg string) {
		t.Helper()
		if _, err := spool.Write("inst-a", 1, "stdout", ts, msg); err != nil {
			t.Fatal(err)
		}
		if err := spool.CloseAll(); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	upload, err := spool.PreparePartUpload("inst-a", day)
	if err != nil {
		t.Fatal(err)
	}
	write("second")
	again, err := spool.PreparePartUpload("inst-a", day)
	if err != nil || again != upload {
		t.Fatalf("second prepare = %q, %v; want the pending upload %q", again, err, upload)
	}
	body, err := os.ReadFile(upload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "second") {
		t.Fatal("the pending upload grew; a retry would no longer match its part")
	}
}

func gunzip(t *testing.T, body []byte) string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
