// shipper_test.go — table-driven tests for the periodic
// shipper goroutine (issue #562). The tests use a fake S3
// (httptest) and a t.TempDir() spool, and assert three
// behaviours the shipper must have:
//
//  1. RunOnce ships every .partial in the spool and removes
//     it; the gzipped object lands in the fake bucket with
//     the expected key + content.
//  2. A failed upload leaves the .partial file alone so the
//     next tick retries; the failure reason lands in the
//     metric counter.
//  3. PurgeOnce removes .jsonl.gz files older than the
//     retention boundary and leaves newer ones alone.

package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeS3Bucket is the test-side counterpart of fakeS3 in
// s3client_test.go: it records every PUT body + key so the
// test can assert the shipper's output, and lets the test
// program the next response status. Used by shipper_test.go
// in isolation to keep the per-file fixtures focused.
type fakeS3Bucket struct {
	objects     map[string][]byte
	failNext    bool
	failStatus  int
	failCode    string
	failMessage string
}

func newFakeS3Bucket() *fakeS3Bucket {
	return &fakeS3Bucket{objects: make(map[string][]byte)}
}

func (f *fakeS3Bucket) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if f.failNext {
			f.failNext = false
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.failStatus)
			_, _ = w.Write([]byte(`{"Code":"` + f.failCode + `","Message":"` + f.failMessage + `"}`))
			return
		}
		// path = /bucket/key
		key := strings.TrimPrefix(r.URL.Path, "/"+bucketFromPath(r.URL.Path))
		key = strings.TrimPrefix(key, "/")
		if r.Method == http.MethodGet && r.URL.Query().Has("prefix") {
			f.writeList(w, r.URL.Query().Get("prefix"))
			return
		}
		if r.Method == http.MethodGet {
			object, ok := f.objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`))
				return
			}
			_, _ = w.Write(object)
			return
		}
		f.objects[key] = body
		w.WriteHeader(200)
	})
}

// writeList answers a ListObjects request with every key under prefix, in
// key order, on one page.
func (f *fakeS3Bucket) writeList(w http.ResponseWriter, prefix string) {
	var keys []string
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("<ListBucketResult><IsTruncated>false</IsTruncated>")
	for _, key := range keys {
		b.WriteString("<Contents><Key>" + key + "</Key></Contents>")
	}
	b.WriteString("</ListBucketResult>")
	_, _ = io.WriteString(w, b.String())
}

// bucketFromPath peels the bucket prefix off /bucket/key.
func bucketFromPath(p string) string {
	p = strings.TrimPrefix(p, "/")
	if i := strings.Index(p, "/"); i >= 0 {
		return p[:i]
	}
	return p
}

// newTestShipper wires a Shipper against the fake S3 + a
// t.TempDir() spool. Returns the shipper, the fake, and a
// recording metrics instance.
func newTestShipper(t *testing.T, retentionDays int) (*Shipper, *fakeS3Bucket, *recordingMetrics) {
	t.Helper()
	root := t.TempDir()
	fake := newFakeS3Bucket()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	s3, err := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	spool := NewSpool(filepath.Join(root, "spool"), 1<<20)
	cfg := Config{
		Bucket:        "mybucket",
		SpoolRoot:     spool.root,
		RetentionDays: retentionDays,
	}
	m := newRecordingMetrics()
	sh, err := NewShipper(cfg, spool, s3, nil, m)
	if err != nil {
		t.Fatalf("NewShipper: %v", err)
	}
	return sh, fake, m
}

// TestShipper_Disabled_NoOp covers the empty-bucket branch:
// RunOnce returns (0, 0, nil) without touching S3 or the
// spool, and Run blocks on ctx.Done(). Matches the apid
// wire-up: FAAS_LOG_ARCHIVE_BUCKET unset = disabled.
func TestShipper_Disabled_NoOp(t *testing.T) {
	spool := NewSpool(t.TempDir(), 1<<20)
	sh, err := NewShipper(Config{}, spool, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewShipper: %v", err)
	}
	n, bytes, err := sh.RunOnce(context.Background())
	if err != nil || n != 0 || bytes != 0 {
		t.Errorf("RunOnce = (%d, %d, %v), want (0, 0, nil)", n, bytes, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sh.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run returned %v on disabled cfg", err)
	}
}

// TestShipper_RunOnce_HappyPath writes two lines to the
// spool, runs RunOnce, and asserts:
//
//   - both .partial files were shipped to the fake bucket;
//   - the local files were removed after the upload;
//   - the metric counters incremented correctly.
func TestShipper_RunOnce_HappyPath(t *testing.T) {
	sh, fake, m := newTestShipper(t, 7)
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	if _, err := sh.spool.Write("inst-a", 1, "stdout", ts, "hello"); err != nil {
		t.Fatalf("Write a: %v", err)
	}
	if _, err := sh.spool.Write("inst-b", 1, "stderr", ts, "world"); err != nil {
		t.Fatalf("Write b: %v", err)
	}
	if err := sh.spool.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	n, _, err := sh.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 2 {
		t.Errorf("shipped=%d, want 2", n)
	}
	if m.filesOK != 2 || m.filesErr != 0 {
		t.Errorf("metrics filesOK=%d filesErr=%d, want 2/0", m.filesOK, m.filesErr)
	}
	if m.bytes == 0 {
		t.Errorf("AddBytesUploaded never called")
	}
	if len(fake.objects) != 2 {
		t.Errorf("bucket has %d objects, want 2", len(fake.objects))
	}
	for k, body := range fake.objects {
		if !strings.HasSuffix(k, ".jsonl.gz") {
			t.Errorf("key %q missing .jsonl.gz suffix", k)
		}
		// gzip-decode and confirm the JSONL line is intact.
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Errorf("gzip.NewReader(%q): %v", k, err)
			continue
		}
		dec := json.NewDecoder(gr)
		var got map[string]any
		if err := dec.Decode(&got); err != nil {
			t.Errorf("decode %q: %v", k, err)
			continue
		}
		if got["msg"] == nil {
			t.Errorf("body %q missing msg field: %v", k, got)
		}
	}
	// After successful upload, the .partial files are gone.
	for _, f := range sh.spool.FilesSnapshot() {
		t.Errorf(".partial still on disk after ship: %s", f.Path)
	}
}

// TestShipper_RunOnce_RetriesOnFailure: a 403 leaves the
// .partial file intact so the next tick retries, and the
// failure reason counter increments.
func TestShipper_RunOnce_RetriesOnFailure(t *testing.T) {
	sh, fake, m := newTestShipper(t, 7)
	ts := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	if _, err := sh.spool.Write("inst-a", 1, "stdout", ts, "hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sh.spool.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	fake.failNext = true
	fake.failStatus = 403
	fake.failCode = "AccessDenied"
	fake.failMessage = "bad key"
	n, _, err := sh.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("shipped=%d on 403, want 0", n)
	}
	if m.filesErr != 1 {
		t.Errorf("filesErr=%d, want 1", m.filesErr)
	}
	if m.failures[FailureReasonAuth] != 1 {
		t.Errorf("failures[auth]=%d, want 1", m.failures[FailureReasonAuth])
	}
	// .partial still on disk for the next tick.
	if got := sh.spool.FilesSnapshot(); len(got) != 1 {
		t.Errorf("after failed upload: %d files, want 1 (retry)", len(got))
	}
	if len(fake.objects) != 0 {
		t.Errorf("bucket has %d objects after failed upload, want 0", len(fake.objects))
	}
}

// TestShipper_PurgeOnce_OldAndNew writes a .jsonl.gz with an
// old mtime and another with a fresh mtime, then asserts
// PurgeOnce removes only the old one.
func TestShipper_PurgeOnce_OldAndNew(t *testing.T) {
	sh, _, _ := newTestShipper(t, 7)
	oldDir := filepath.Join(sh.cfg.SpoolRoot, "inst-old", "2026", "07", "01")
	if err := os.MkdirAll(oldDir, 0o750); err != nil {
		t.Fatalf("MkdirAll old: %v", err)
	}
	oldPath := filepath.Join(oldDir, "2026-07-01.jsonl.gz")
	if err := os.WriteFile(oldPath, []byte("old"), 0o640); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	oldT := time.Now().AddDate(0, 0, -30)
	if err := os.Chtimes(oldPath, oldT, oldT); err != nil {
		t.Fatalf("Chtimes old: %v", err)
	}

	newDir := filepath.Join(sh.cfg.SpoolRoot, "inst-new", "2026", "08", "08")
	if err := os.MkdirAll(newDir, 0o750); err != nil {
		t.Fatalf("MkdirAll new: %v", err)
	}
	newPath := filepath.Join(newDir, "2026-08-08.jsonl.gz")
	if err := os.WriteFile(newPath, []byte("new"), 0o640); err != nil {
		t.Fatalf("WriteFile new: %v", err)
	}

	removed, err := sh.PurgeOnce(context.Background())
	if err != nil {
		t.Fatalf("PurgeOnce: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed=%d, want 1", removed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old file still exists: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("new file removed: %v", err)
	}
}

// TestShipper_PurgeOnce_SkipsPartial pins the bug-prevention
// shape: the purger must NOT remove .partial files (they
// haven't been shipped yet). A regression that sweeps
// .partial would cause data loss during a retention sweep.
func TestShipper_PurgeOnce_SkipsPartial(t *testing.T) {
	sh, _, _ := newTestShipper(t, 7)
	dir := filepath.Join(sh.cfg.SpoolRoot, "inst", "2026", "07", "01")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	partial := filepath.Join(dir, "log-2026-07-01.jsonl.partial")
	if err := os.WriteFile(partial, []byte("pending"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	oldT := time.Now().AddDate(0, 0, -30)
	if err := os.Chtimes(partial, oldT, oldT); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	removed, err := sh.PurgeOnce(context.Background())
	if err != nil {
		t.Fatalf("PurgeOnce: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed=%d, want 0 (must not sweep .partial)", removed)
	}
	if _, err := os.Stat(partial); err != nil {
		t.Errorf("partial removed: %v", err)
	}
}

// TestShipper_NewShipper_Validation covers the constructor's
// fail-closed branches.
func TestShipper_NewShipper_Validation(t *testing.T) {
	if _, err := NewShipper(Config{Bucket: "b"}, nil, &S3Client{}, nil, nil); err == nil {
		t.Errorf("nil spool: want error, got nil")
	}
	s3, _ := NewS3Client("https://x", "us-east-1", "b", "k", "s")
	if _, err := NewShipper(Config{Bucket: "b"}, NewSpool(t.TempDir(), 1<<20), nil, nil, nil); err == nil {
		t.Errorf("bucket set but s3 nil: want error, got nil")
	}
	if _, err := NewShipper(Config{Bucket: "b"}, NewSpool(t.TempDir(), 1<<20), s3, nil, nil); err != nil {
		t.Errorf("valid config: %v", err)
	}
}

// TestShipper_ClassifyFailure covers the reason-bucket
// mapping directly, so a future addition of a new S3 error
// code doesn't silently land in the wrong bucket.
func TestShipper_ClassifyFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"403 AccessDenied", &Permanent{StatusCode: 403, Code: "AccessDenied"}, FailureReasonAuth},
		{"403 SignatureDoesNotMatch", &Permanent{StatusCode: 403, Code: "SignatureDoesNotMatch"}, FailureReasonAuth},
		{"429 TooManyRequests", &Permanent{StatusCode: 429, Code: "TooManyRequests"}, FailureReasonThrottle},
		{"429 SlowDown", &Permanent{StatusCode: 429, Code: "SlowDown"}, FailureReasonThrottle},
		{"400 EntityTooLarge", &Permanent{StatusCode: 400, Code: "EntityTooLarge"}, FailureReasonSize},
		{"400 BodyLengthMismatch", &Permanent{StatusCode: 0, Code: "BodyLengthMismatch"}, FailureReasonBodyLength},
		{"spool full", ErrSpoolFull, FailureReasonSpoolFull},
		{"500 transient", errPerm(500), FailureReasonNetwork},
		{"nil error", nil, FailureReasonOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyFailure(tc.err); got != tc.want {
				t.Errorf("classifyFailure = %q, want %q", got, tc.want)
			}
		})
	}
}

// errPerm constructs a *Permanent with a bare StatusCode (no
// matching Code) so the test exercises the default-network
// branch of classifyFailure.
func errPerm(status int) error {
	return &Permanent{StatusCode: status, Code: "Unknown"}
}
