package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// waitForBody polls the recorder until want appears or the deadline passes.
func waitForBody(t *testing.T, rec *flusherRecorder, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.body.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in %q", want, rec.body.String())
}

// An archived day streams: the first record reaches the client while the
// object is still downloading, and heartbeats keep flowing while the bucket
// is slow, instead of the whole object being held in memory first.
func TestArchiveStream_DeliversLinesBeforeTheObjectFinishes(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte(`{"seq":1,"stream":"stdout","ts":"2026-08-07T12:00:00Z","msg":"first"}` + "\n"))
		_ = gz.Flush()
		w.(http.Flusher).Flush()
		<-release
		_, _ = gz.Write([]byte(`{"seq":2,"stream":"stdout","ts":"2026-08-07T12:00:01Z","msg":"second"}` + "\n"))
		_ = gz.Close()
	}))
	t.Cleanup(srv.Close)
	h := &ArchiveLogsHandler{
		Scope: ownedArchiveScope(), S3: newArchiveTestClient(t, srv), Bucket: "test-bucket",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Backstop: 10 * time.Second, Heartbeat: 20 * time.Millisecond,
	}
	rec := newFlusherRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/apps/test-app/logs?archive=1&instance=inst-abc&date="+archiveTestDate(), nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.streamUnauth(rec, r, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "test-app-id")
	}()

	waitForBody(t, rec, `"line":"first"`)
	waitForBody(t, rec, ":\n\n")
	if strings.Contains(rec.body.String(), "event: end") {
		t.Fatalf("stream ended while the object was still downloading: %q", rec.body.String())
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not finish after the object completed")
	}
	out := rec.body.String()
	if !strings.Contains(out, `"line":"second"`) || !strings.Contains(out, `"reason":"archive_complete"`) {
		t.Fatalf("want both records and archive_complete, got %q", out)
	}
}

// A 1 MiB log line can grow to about 6 MiB as an escaped JSON record; it
// still renders instead of failing the rest of the day.
func TestArchiveStream_EscapedMaximumLineFits(t *testing.T) {
	// The spool writes records with json.Marshal, which escapes each "<" of
	// the line as a six-byte < sequence.
	escaped := string([]byte{'\\', 'u', '0', '0', '3', 'c'})
	lineLen := 1 << 20
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(`{"seq":1,"stream":"stdout","ts":"2026-08-07T12:00:00Z","msg":"` + strings.Repeat(escaped, lineLen) + `"}` + "\n"))
	_ = gz.Close()
	h := &ArchiveLogsHandler{
		Scope: ownedArchiveScope(), S3: newArchiveTestClient(t, newFakeS3(t, http.StatusOK, buf.Bytes())), Bucket: "test-bucket",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Backstop: 10 * time.Second,
	}
	out := driveStream(t, h, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "archive=1&instance=inst-abc&date="+archiveTestDate())
	if !strings.Contains(out, `"reason":"archive_complete"`) {
		t.Fatalf("want archive_complete, got terminal %q", out[max(0, len(out)-200):])
	}
	// The SSE payload is JSON too, so the line arrives escaped again.
	if frames, chars := strings.Count(out, "event: log\n"), strings.Count(out, escaped); frames != 1 || chars != lineLen {
		t.Fatalf("the long line was not rendered: %d log frames, %d escaped characters, want 1 and %d", frames, chars, lineLen)
	}
}

// An object cut off mid-stream ends degraded: the client saw only part of
// the day, so it must not read as a clean success.
func TestArchiveStream_TruncatedObjectIsDegraded(t *testing.T) {
	full := gzipJSONL(t, []string{
		`{"seq":1,"stream":"stdout","ts":"2026-08-07T12:00:00Z","msg":"` + strings.Repeat("x", 4096) + `"}`,
		`{"seq":2,"stream":"stdout","ts":"2026-08-07T12:00:01Z","msg":"` + strings.Repeat("y", 4096) + `"}`,
	})
	h := &ArchiveLogsHandler{
		Scope: ownedArchiveScope(), S3: newArchiveTestClient(t, newFakeS3(t, http.StatusOK, full[:len(full)/2])), Bucket: "test-bucket",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Backstop: 10 * time.Second,
	}
	out := driveStream(t, h, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "archive=1&instance=inst-abc&date="+archiveTestDate())
	if !strings.Contains(out, `"reason":"archive_degraded"`) {
		t.Fatalf("want archive_degraded for a truncated object, got %q", out[max(0, len(out)-200):])
	}
}
