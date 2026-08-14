// app_logs_archive_test.go — whitebox tests for the log-archive
// read-back handler (issue #562 PR-B). The handler's Auth chain
// is exercised in pkg/auth/middleware_test.go; here we drive
// stream() directly with a constructed state.Account + a fake
// S3 server (httptest) so the receive-pump / plan-gate / query
// validation paths are pinned in isolation. Mirrors the
// cmd/gatewayd-internal/app_logs_test.go whitebox seam (the
// live handler does the same thing against a controllableScheddClient).
//
// Test surface:
//
//   - HappyPath: gzip→JSONL→SSE — a small .jsonl.gz ships
//     through S3, the handler reads it back, and three
//     `event: log` frames appear in the recorder body.
//   - FreePlan_Returns402: Plan.LogArchiveEnabled() == false
//     (Free) → 402 + CodePlanLogArchiveNotAllowed. Verifies the
//     plan gate fires BEFORE the auth chain so a Free customer
//     never sees the bucket-proxy surface.
//   - S3NotFound_ArchiveMissing: S3 returns 404 → terminal
//     reason archive_missing. The handler never touches the
//     gzip pipe; the S3 error is the wire-level signal.
//   - S3NetworkError_ArchiveDegraded: S3 returns 500 → terminal
//     reason archive_degraded. Distinguished from
//     archive_missing so the SDK can branch.
//   - InvalidDate: ?date=not-a-date → 400 + log_archive_invalid_query.
//     Defends against path-traversal-shaped values before they
//     reach S3.
//   - FutureDate: ?date=tomorrow → 403 + log_archive_retention_exceeded.
//     A customer can't probe beyond today.
//   - RetentionCap: TestArchiveStream_RetentionCap_HobbyBoundary
//     pins the boundary (today-6d → 200, today-8d → 403).
//     All other tests use dateForInsideCap (today-1d) so they
//     stay date-proof regardless of when CI runs them.

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

// gzipJSONL is the test helper that produces the bytes the
// fake S3 server hands to GetObject. Each line is one
// spoolLine JSON blob with a unique seq so the assertions can
// pin the rendered frame order. The buffer is gzipped in
// memory (no tempfile) so the test runs without filesystem
// state.
func gzipJSONL(t *testing.T, lines []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	for _, line := range lines {
		if _, err := gz.Write([]byte(line + "\n")); err != nil {
			t.Fatalf("gzip write: %v", err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// newFakeS3 wires a tiny httptest server that mimics the
// subset of S3 GetObject the bucket-proxy read-back handler
// uses. status programs the response (200 / 404 / 500);
// body is the bytes sent on a 200. The fake signs responses
// with the same shape real S3 does (SigV4 verification is the
// test's burden, not the fake's — the logarchive.S3Client
// signs every request and the fake doesn't validate).
func newFakeS3(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(status)
			if body != nil {
				_, _ = w.Write(body)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newArchiveTestClient wraps logarchive.NewS3Client with the
// fake S3 endpoint + test creds. Returns nil on construction
// failure so the test can fail with a clear message rather
// than a nil deref.
func newArchiveTestClient(t *testing.T, srv *httptest.Server) *logarchive.S3Client {
	t.Helper()
	c, err := logarchive.NewS3Client(
		srv.URL, "us-east-1", "test-bucket",
		"AKIATEST", "secret-test",
	)
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	return c
}

// newArchivePlaceholderClient returns a non-nil client that
// points at an unreachable address. Used by tests that need
// to pass the S3-nil gate so they can exercise the
// downstream validation branches (query params, retention
// cap) without standing up a fake S3 server. The placeholder
// is never actually dialed because the test exits at the
// validation step.
func newArchivePlaceholderClient(t *testing.T) *logarchive.S3Client {
	t.Helper()
	c, err := logarchive.NewS3Client(
		"http://127.0.0.1:1", "us-east-1", "test-bucket",
		"AKIATEST", "secret-test",
	)
	if err != nil {
		t.Fatalf("NewS3Client (placeholder): %v", err)
	}
	return c
}

// driveStream runs the archive handler's streamUnauth() against
// a fresh recorder with a constructed account + an http.Request
// carrying the given query. Auth is nil — streamUnauth() skips
// LoadApp (the Auth field would nil-deref) and tests verify the
// plan-gate / query-validation / S3-error paths directly.
// Returns the response body.
func driveStream(t *testing.T, h *ArchiveLogsHandler, account state.Account, query string) string {
	t.Helper()
	rec := newFlusherRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/apps/test-app/logs?"+query, nil)
	h.streamUnauth(rec, r, account, "test-app-id")
	return rec.body.String()
}

// dateForInsideCap returns a YYYY-MM-DD string that
// withinRetention will accept for any plan with a non-zero
// LogArchiveRetentionDaysMax (Hobby=7, Pro=30, Scale=90).
// Always picks today-1d so tests stay date-proof: a fixture
// written today still satisfies the per-plan window tomorrow,
// next week, and next year. Mirrors the relative-date
// convention already in use at lines 322-325 (boundary test)
// and line 358 (future-date test), plus
// pkg/meter/dunning_test.go:135/162/210 and friends.
func dateForInsideCap() string {
	return time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
}

// TestArchiveStream_HappyPath is the smoke test: gzipped JSONL
// in, three `event: log` SSE frames + a terminal
// `event: end archive_complete` out. The instance id +
// date + bucket key wire shape is pinned through the fake
// server's request log so a future drift in
// archiveObjectKey's layout fails loud.
func TestArchiveStream_HappyPath(t *testing.T) {
	lines := []string{
		`{"seq":1,"stream":"stdout","ts":"2026-08-07T12:00:00Z","msg":"hello"}`,
		`{"seq":2,"stream":"stdout","ts":"2026-08-07T12:00:01Z","msg":"world"}`,
		`{"seq":3,"stream":"stderr","ts":"2026-08-07T12:00:02Z","msg":"boom"}`,
	}
	body := gzipJSONL(t, lines)

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	h := &ArchiveLogsHandler{
		S3:       newArchiveTestClient(t, srv),
		Bucket:   "test-bucket",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backstop: 5 * time.Second,
	}
	day := dateForInsideCap()
	body_out := driveStream(t, h, state.Account{ID: "acct-1", Plan: api.PlanHobby},
		"archive=1&instance=inst-abc&date="+day)

	// The key shape is {prefix}/{instance}/{YYYY}/{MM}/{DD}.jsonl.gz.
	// archiveObjectKey slices day for YYYY/MM; reuse the same value
	// here so a fixture rewrite stays consistent.
	wantPath := "/test-bucket/faas-logs/inst-abc/" + day[:4] + "/" + day[5:7] + "/" + day + ".jsonl.gz"
	if gotPath != wantPath {
		t.Errorf("S3 key: got %q, want %q", gotPath, wantPath)
	}
	// Pin three log frames + terminal.
	wantCount := strings.Count(body_out, "event: log\ndata: ")
	if wantCount != 3 {
		t.Errorf("log frames: got %d, want 3\nbody: %s", wantCount, body_out)
	}
	if !strings.Contains(body_out, `"instance":"inst-abc"`) {
		t.Errorf("missing instance in frame payload: %s", body_out)
	}
	if !strings.Contains(body_out, `"stream":"stderr"`) {
		t.Errorf("missing stream in frame payload: %s", body_out)
	}
	if !strings.Contains(body_out, `"reason":"archive_complete"`) {
		t.Errorf("missing archive_complete terminal: %s", body_out)
	}
}

// TestArchiveStream_FreePlan_Returns402 pins the plan-gate.
// Free (LogArchiveEnabled() == false) must surface 402 +
// CodePlanLogArchiveNotAllowed WITHOUT touching S3. The
// fake server is intentionally not wired; the test asserts
// the request never lands by relying on the handler's
// early-return order: plan gate → store → S3.
func TestArchiveStream_FreePlan_Returns402(t *testing.T) {
	h := &ArchiveLogsHandler{
		// S3 nil + a non-zero Bucket — the handler must refuse
		// on plan alone, not on the S3 nil-check.
		S3:     nil,
		Bucket: "test-bucket",
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rec := newFlusherRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/v1/apps/test-app/logs?archive=1&instance=inst-abc&date=2026-08-07", nil)
	h.streamUnauth(rec, r, state.Account{ID: "acct-1", Plan: api.PlanFree}, "test-app-id")

	if rec.h.Get("Content-Type") != "application/problem+json" {
		t.Errorf("Content-Type: got %q, want application/problem+json",
			rec.h.Get("Content-Type"))
	}
	if !strings.Contains(rec.body.String(), "plan_log_archive_not_allowed") {
		t.Errorf("body missing plan_log_archive_not_allowed: %s", rec.body.String())
	}
	if !strings.Contains(rec.body.String(), "free") {
		t.Errorf("body missing plan name: %s", rec.body.String())
	}
}

// TestArchiveStream_S3NotFound_ArchiveMissing pins the
// not-found path: S3 returns 404, the handler emits
// `event: end archive_missing` so the SDK's branch on
// archive_missing surfaces a "no logs for that day" UX.
// The 404 maps to a *Permanent in the S3 client, which
// archiveTerminalForError classifies as archive_missing.
func TestArchiveStream_S3NotFound_ArchiveMissing(t *testing.T) {
	srv := newFakeS3(t, http.StatusNotFound, []byte(`<Error><Code>NoSuchKey</Code></Error>`))
	h := &ArchiveLogsHandler{
		S3:       newArchiveTestClient(t, srv),
		Bucket:   "test-bucket",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backstop: 5 * time.Second,
	}
	body_out := driveStream(t, h, state.Account{ID: "acct-1", Plan: api.PlanHobby},
		"archive=1&instance=inst-abc&date="+dateForInsideCap())

	if !strings.Contains(body_out, `"reason":"archive_missing"`) {
		t.Errorf("body missing archive_missing: %s", body_out)
	}
}

// TestArchiveStream_S3ServerError_ArchiveDegraded pins the
// 5xx path: S3 returns 500, the handler emits
// `event: end archive_degraded`. The SDK's branch on
// archive_degraded surfaces a retry hint.
func TestArchiveStream_S3ServerError_ArchiveDegraded(t *testing.T) {
	srv := newFakeS3(t, http.StatusInternalServerError, []byte(`<Error><Code>InternalError</Code></Error>`))
	h := &ArchiveLogsHandler{
		S3:       newArchiveTestClient(t, srv),
		Bucket:   "test-bucket",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backstop: 5 * time.Second,
	}
	body_out := driveStream(t, h, state.Account{ID: "acct-1", Plan: api.PlanHobby},
		"archive=1&instance=inst-abc&date="+dateForInsideCap())

	if !strings.Contains(body_out, `"reason":"archive_degraded"`) {
		t.Errorf("body missing archive_degraded: %s", body_out)
	}
}

// TestArchiveStream_InvalidDate pins the date regex guard.
// A non-YYYY-MM-DD value fails validation BEFORE the S3
// fetch — defends against path-traversal-shaped strings.
func TestArchiveStream_InvalidDate(t *testing.T) {
	h := &ArchiveLogsHandler{
		// Placeholder S3: validation fires before S3 is touched.
		S3:     newArchivePlaceholderClient(t),
		Bucket: "test-bucket",
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rec := newFlusherRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/v1/apps/test-app/logs?archive=1&instance=inst-abc&date=not-a-date", nil)
	h.streamUnauth(rec, r, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "test-app-id")

	if !strings.Contains(rec.body.String(), "log_archive_invalid_query") {
		t.Errorf("body missing log_archive_invalid_query: %s", rec.body.String())
	}
}

// TestArchiveStream_InvalidInstance pins the instance
// regex guard. The character class [A-Za-z0-9._-] is the
// hard limit; anything else fails validation.
func TestArchiveStream_InvalidInstance(t *testing.T) {
	h := &ArchiveLogsHandler{
		S3:     newArchivePlaceholderClient(t),
		Bucket: "test-bucket",
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rec := newFlusherRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/v1/apps/test-app/logs?archive=1&instance=../etc/passwd&date=2026-08-07", nil)
	h.streamUnauth(rec, r, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "test-app-id")

	if !strings.Contains(rec.body.String(), "log_archive_invalid_query") {
		t.Errorf("body missing log_archive_invalid_query: %s", rec.body.String())
	}
}

// TestArchiveStream_RetentionCap_HobbyBoundary pins the
// per-plan retention cap (issue #562 risk #7). Hobby =
// 7 days. A date 8 days ago must be refused; 6 days ago
// passes through. The boundary is +1 inclusive: Hobby
// customers see days [today-6, today].
func TestArchiveStream_RetentionCap_HobbyBoundary(t *testing.T) {
	srv := newFakeS3(t, http.StatusOK, gzipJSONL(t, []string{`{"seq":1,"stream":"stdout","ts":"2026-08-07T12:00:00Z","msg":"x"}`}))
	h := &ArchiveLogsHandler{
		S3:       newArchiveTestClient(t, srv),
		Bucket:   "test-bucket",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backstop: 5 * time.Second,
	}
	now := time.Now().UTC()
	within := now.AddDate(0, 0, -6).Format("2006-01-02")
	outside := now.AddDate(0, 0, -8).Format("2006-01-02")
	t.Run("inside cap passes", func(t *testing.T) {
		rec := newFlusherRecorder()
		r := httptest.NewRequest(http.MethodGet,
			"/v1/apps/test-app/logs?archive=1&instance=inst-abc&date="+within, nil)
		h.streamUnauth(rec, r, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "test-app-id")
		if !strings.Contains(rec.body.String(), "archive_complete") {
			t.Errorf("inside-cap query should reach S3: %s", rec.body.String())
		}
	})
	t.Run("outside cap refused", func(t *testing.T) {
		rec := newFlusherRecorder()
		r := httptest.NewRequest(http.MethodGet,
			"/v1/apps/test-app/logs?archive=1&instance=inst-abc&date="+outside, nil)
		h.streamUnauth(rec, r, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "test-app-id")
		if !strings.Contains(rec.body.String(), "log_archive_retention_exceeded") {
			t.Errorf("outside-cap query should refuse: %s", rec.body.String())
		}
	})
}

// TestArchiveStream_FutureDateRefused pins the future-date
// guard. A customer asking for tomorrow's logs gets a 403
// even if tomorrow is technically within the retention
// window — there's no archive for a future day.
func TestArchiveStream_FutureDateRefused(t *testing.T) {
	h := &ArchiveLogsHandler{
		S3:     newArchivePlaceholderClient(t),
		Bucket: "test-bucket",
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	rec := newFlusherRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/v1/apps/test-app/logs?archive=1&instance=inst-abc&date="+tomorrow, nil)
	h.streamUnauth(rec, r, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "test-app-id")

	if !strings.Contains(rec.body.String(), "log_archive_retention_exceeded") {
		t.Errorf("body missing retention_exceeded for future date: %s", rec.body.String())
	}
}

// TestArchiveStream_MissingQueryParams pins the
// required-param guard. Both ?instance= and ?date= are
// required for the archive path; missing either returns
// 400 + log_archive_invalid_query.
func TestArchiveStream_MissingQueryParams(t *testing.T) {
	h := &ArchiveLogsHandler{
		S3:     newArchivePlaceholderClient(t),
		Bucket: "test-bucket",
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	cases := []string{
		"archive=1",
		"archive=1&instance=inst-abc",
		"archive=1&date=2026-08-07",
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			rec := newFlusherRecorder()
			r := httptest.NewRequest(http.MethodGet,
				"/v1/apps/test-app/logs?"+q, nil)
			h.streamUnauth(rec, r, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "test-app-id")
			if !strings.Contains(rec.body.String(), "log_archive_invalid_query") {
				t.Errorf("body missing log_archive_invalid_query for q=%q: %s",
					q, rec.body.String())
			}
		})
	}
}

// TestArchiveStream_MalformedJSONLine pins the degraded-
// mid-stream path. A gzip body with a malformed line must
// surface archive_degraded, not archive_complete. The
// handler emits whatever lines it can parse, then the
// terminal carries the degraded reason.
func TestArchiveStream_MalformedJSONLine(t *testing.T) {
	lines := []string{
		`{"seq":1,"stream":"stdout","ts":"2026-08-07T12:00:00Z","msg":"ok"}`,
		`{not json`,
	}
	body := gzipJSONL(t, lines)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	h := &ArchiveLogsHandler{
		S3:       newArchiveTestClient(t, srv),
		Bucket:   "test-bucket",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backstop: 5 * time.Second,
	}
	body_out := driveStream(t, h, state.Account{ID: "acct-1", Plan: api.PlanHobby},
		"archive=1&instance=inst-abc&date="+dateForInsideCap())

	if !strings.Contains(body_out, `"reason":"archive_degraded"`) {
		t.Errorf("malformed JSON should degrade: %s", body_out)
	}
}

// TestArchiveStream_NilS3AndBucket_ServiceUnavailable pins
// the unconfigured handler. When archiveS3 is nil and the
// bucket is empty (operator hasn't unsealed the creds
// envelope yet), a Hobby customer gets a 503 + a stable
// code instead of a nil deref. The free-plan gate fires
// FIRST, so this branch is only reachable on Hobby+.
func TestArchiveStream_NilS3AndBucket_ServiceUnavailable(t *testing.T) {
	h := &ArchiveLogsHandler{
		S3:     nil,
		Bucket: "",
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rec := newFlusherRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/v1/apps/test-app/logs?archive=1&instance=inst-abc&date=2026-08-07", nil)
	h.streamUnauth(rec, r, state.Account{ID: "acct-1", Plan: api.PlanHobby}, "test-app-id")

	if !strings.Contains(rec.body.String(), "log_archive_unconfigured") {
		t.Errorf("body missing log_archive_unconfigured: %s", rec.body.String())
	}
}

// TestArchiveObjectKey pins the bucket-key layout. A
// future drift in archiveObjectKey breaks the
// TestArchiveStream_HappyPath assertion through the
// fake-server request log; this pin makes the contract
// loud + standalone.
func TestArchiveObjectKey(t *testing.T) {
	cases := []struct {
		instance, day, want string
	}{
		{"inst-abc", "2026-08-07", "faas-logs/inst-abc/2026/08/2026-08-07.jsonl.gz"},
		{"i", "2025-01-01", "faas-logs/i/2025/01/2025-01-01.jsonl.gz"},
		// Defensive: a sub-10-char day falls back to the flat
		// layout so a malformed caller can't sneak a `/` into
		// the path.
		{"inst", "x", "faas-logs/inst/x.jsonl.gz"},
	}
	for _, tc := range cases {
		got := archiveObjectKey(tc.instance, tc.day)
		if got != tc.want {
			t.Errorf("archiveObjectKey(%q, %q) = %q, want %q", tc.instance, tc.day, got, tc.want)
		}
	}
}

// TestArchiveTerminalForError pins the error→reason mapping.
// Renaming any of the three reason strings breaks the SDK
// decoder, so the mapping is the load-bearing contract.
func TestArchiveTerminalForError(t *testing.T) {
	if got := archiveTerminalForError(nil); got != "archive_complete" {
		t.Errorf("nil err: got %q, want archive_complete", got)
	}
	perm := &logarchive.Permanent{StatusCode: 404, Code: "NoSuchKey"}
	if got := archiveTerminalForError(perm); got != "archive_missing" {
		t.Errorf("Permanent: got %q, want archive_missing", got)
	}
	perm403 := &logarchive.Permanent{StatusCode: 403, Code: "AccessDenied"}
	if got := archiveTerminalForError(perm403); got != "archive_missing" {
		t.Errorf("AccessDenied Permanent: got %q, want archive_missing", got)
	}
	other := context.Canceled
	if got := archiveTerminalForError(other); got != "archive_degraded" {
		t.Errorf("non-Permanent: got %q, want archive_degraded", got)
	}
}

// TestArchiveStream_FramePayloadShape pins the
// {seq, instance, stream, line, written_at} wire keys
// the SDK decodes. A future drift in any of these names
// breaks every customer who reads archive streams; this
// pin makes the contract loud.
func TestArchiveStream_FramePayloadShape(t *testing.T) {
	lines := []string{
		`{"seq":42,"stream":"stdout","ts":"2026-08-07T12:00:00Z","msg":"hello"}`,
	}
	body := gzipJSONL(t, lines)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	h := &ArchiveLogsHandler{
		S3:       newArchiveTestClient(t, srv),
		Bucket:   "test-bucket",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backstop: 5 * time.Second,
	}
	body_out := driveStream(t, h, state.Account{ID: "acct-1", Plan: api.PlanHobby},
		"archive=1&instance=inst-abc&date="+dateForInsideCap())

	// Extract the first event: log payload to inspect the keys.
	i := strings.Index(body_out, "data: {")
	if i < 0 {
		t.Fatalf("no log frame in body: %s", body_out)
	}
	end := strings.Index(body_out[i:], "\n\n")
	if end < 0 {
		t.Fatalf("no frame terminator in body: %s", body_out)
	}
	payload := body_out[i+len("data: ") : i+end]
	var got map[string]any
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload not JSON: %v\npayload: %s", err, payload)
	}
	for _, key := range []string{"seq", "instance", "stream", "line", "written_at"} {
		if _, ok := got[key]; !ok {
			t.Errorf("payload missing key %q: %v", key, got)
		}
	}
	if got["seq"].(float64) != 42 {
		t.Errorf("seq: got %v, want 42", got["seq"])
	}
	if got["line"].(string) != "hello" {
		t.Errorf("line: got %v, want hello", got["line"])
	}
	if got["stream"].(string) != "stdout" {
		t.Errorf("stream: got %v, want stdout", got["stream"])
	}
	if got["instance"].(string) != "inst-abc" {
		t.Errorf("instance: got %v, want inst-abc", got["instance"])
	}
}
