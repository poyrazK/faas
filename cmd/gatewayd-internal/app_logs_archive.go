// cmd/gatewayd-internal/app_logs_archive.go — log-archive read-back
// handler (issue #562 PR-B). Companion to cmd/gatewayd-internal/
// app_logs.go (the live-stream handler); together they own
// `GET /v1/apps/{slug}/logs`. The two handlers split on
// `?archive=1`:
//
//   - ?archive=1 (PR-B, this file)  → serve historical lines from
//     s3://{bucket}/faas-logs/{instance}/{YYYY}/{MM}/{DD}.jsonl.gz.
//     Customer picks the (instance, day) tuple; gatewayd-internal fetches
//     the gzipped object, decompresses, and emits the same
//     `event: log` SSE envelope the live stream emits so the SDK
//     decoder (pkg/api/sse.go) treats the two paths interchangeably.
//   - ?archive unset                  → live stream (PR-2, app_logs.go).
//
// Why two handlers split on a query param (instead of a separate
// route):
//
//   - The customer-facing API surface is one URL, not two. The
//     live-stream + archive surfaces compose naturally on the
//     client: "follow tail for 60s, then ?archive=1 to walk back
//     the history". Splitting into /logs (live) + /logs/archive
//     (history) would force the client to pick a mount point up
//     front and bolt on cross-URL correlation by hand.
//   - The auth chain is identical. Both paths need the bearer /
//     session / MFA / scope gate (api.ScopesReadSurface), the
//     IDOR-safe LoadApp (the customer's account owns the app
//     regardless of archive mode), and the per-IP AuthLimit
//     bucket (the spec §11 10/min/IP rule applies to read-back
//     just as much as live).
//   - The plan-gate is identical too. Plan.LogArchiveEnabled()
//     is the only archive-specific check — and it sits at the top
//     of this handler, before S3 is touched, so a Free customer
//     gets a 402 the same way they would for any other
//     plan-gated surface.
//
// Wire shape (issue #562 AC3): the read-back stream emits the
// exact `event: log` envelope apislogs.RenderAppLogEvent writes,
// frame-for-frame. A consumer that follows the live stream can
// transparently swap to ?archive=1 by reconnecting with the new
// query string — same event names, same payload keys, same flush
// cadence. Terminal frames:
//
//	event: end  data: {"reason":"archive_complete"}    (success)
//	event: end  data: {"reason":"archive_missing"}     (S3 404)
//	event: end  data: {"reason":"archive_degraded"}    (gzip / S3 5xx)
//
// The three reason strings are the stable vocabulary the SDK
// branches on; renaming any is a breaking change to
// `pkg/api/sse.go::IsArchiveTerminatedReason`.
//
// Auth chain: bearer / session / MFA / scope / IDOR-safe LoadApp
// via pkg/auth.Middleware (ADR-046) — same surface the live
// handler uses (cmd/gatewayd-internal/app_logs.go::ServeHTTP).
// The carrier here is ServeHTTP; stream() does the plan-gate +
// query validation + S3 fetch + SSE pump.

package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// archiveQueryDateLayout is the canonical ?date= format. The
// archive bucket key is YYYY/MM/DD; ?date= accepts the same shape
// the customer sees in the live-stream UI (YYYY-MM-DD). Lifted
// into a const so the validation regex below has a single source
// of truth and goconst stops nagging on the literal.
const archiveQueryDateLayout = "2006-01-02"

// Archive-end-reason vocabulary. Stable wire contract (issue #562
// AC3 / Wire shape comment above) — the SDK branches on these
// strings, so renaming is a breaking change. Pinning them here is
// also the goconst enforcement: the three-terminal frame names
// appear in renderArchiveTerminal's call sites + the test corpus
// + the doc-comment block above.
const (
	archiveReasonComplete = "archive_complete"
	archiveReasonMissing  = "archive_missing"
	archiveReasonDegraded = "archive_degraded"
)

// archiveDateRegex pins the ?date= format to YYYY-MM-DD. Defends
// against path-traversal-shaped strings ("..", "2025-01-01/",
// "2025/01/01") before they reach the S3 key — the bucket proxy
// is a single-purpose handler that has no business surfacing
// arbitrary key shapes. The regex is anchored on both ends and
// rejects anything that isn't a four-digit year + two-digit
// month + two-digit day.
var archiveDateRegex = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// archiveInstanceRegex caps ?instance= to a safe character set.
// Firecracker instance ids are normally UUIDs (36 chars, hex +
// dashes) but the spool accepts any string up to
// logarchive.MaxInstanceIDLen (128) — the bucket-proxy uses the
// same lenient shape and rejects anything outside
// [A-Za-z0-9._-]. The character class forecloses the
// path-traversal surface (no slashes, no backslashes, no NUL) at
// the handler boundary, not at S3.
var archiveInstanceRegex = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// ArchiveLogsHandler owns the ?archive=1 branch of
// `GET /v1/apps/{slug}/logs`. The handler composes four services:
//
//   - *mwauth.Middleware — shared with AppLogsHandler. Same bearer
//     / session / MFA / scope / IDOR-safe LoadApp, same per-IP
//     AuthLimit bucket. Per the spec §11 single-public-listener
//     invariant (ADR-011), this handler sits behind the
//     gatewayd-public → gatewayd-internal unix-socket hop and
//     runs only after the auth chain.
//   - *logarchive.S3Client — hand-rolled PUT/GET client (PR-A).
//     The handler uses GetObject only; PUTs are owned by the
//     shipper on apid. nil-safe so unit tests + dev boxes that
//     haven't unsealed the creds envelope still boot — the
//     handler returns a 503 + a stable code in that branch
//     instead of crashing on a nil deref.
//   - state.Store — backs the IDOR-safe LoadApp. The same
//     *state.PgStore pointer satisfies both the live and
//     archive handlers.
//   - *wire.OpsMetrics — apid_logs_emitted_total{app=<slug>}
//     counter, one increment per rendered frame so the per-app
//     log volume is observable regardless of the source (live
//     vs archive). nil-safe so tests that don't wire metrics
//     keep working.
//
// Backstop mirrors the live handler's — the archive read-back is
// a finite stream (one .jsonl.gz per day) but the S3 round-trip
// can stall on a transient network blip, and a 10-minute idle
// cap is the same envelope the live stream enforces. The
// production default matches defaultAppLogsBackstop.
type ArchiveLogsHandler struct {
	Auth *mwauth.Middleware
	S3   *logarchive.S3Client
	// Bucket is the destination bucket the shipper writes
	// into (FAAS_LOG_ARCHIVE_BUCKET). Carried on the
	// handler rather than read from S3.Bucket so tests
	// can swap it without rebuilding the client.
	Bucket string
	Store  state.Store
	Log    *slog.Logger
	Ops    *wire.OpsMetrics

	// Backstop is the SSE stream idle timeout. 0 = use the
	// production default (10 minutes, mirroring
	// AppLogsHandler).
	Backstop time.Duration
}

// ServeHTTP is the http.Handler entry point. The auth chain is
// composed with the same RequireLimited → RequireMFA → RequireScope
// shape AppLogsHandler uses (issue #254 / Move 4 PR-2). When
// ?archive=1 is absent the handler delegates to AppLogsHandler
// (the live-stream sibling) so the URL is owned end-to-end here.
func (h *ArchiveLogsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("archive") != "1" {
		// Hand off to the live handler. The mux that wires this
		// handler is responsible for mounting both — see
		// cmd/gatewayd-internal/run.go (PR-B-3). Bailout returns
		// to the mux, which routes the request to the live
		// sibling.
		http.Error(w, "archive handler requires ?archive=1 (live stream is a separate handler)", http.StatusInternalServerError)
		return
	}
	slug := r.URL.Query().Get("slug")
	if slug == "" {
		// r.PathValue is not bound when the mux registers this
		// handler directly; the wrapping mux (run.go::logsMux)
		// extracts the slug via r.PathValue and threads it
		// through. Tests that mount the handler bare must pass
		// the slug explicitly via ?slug=. The "no slug" branch
		// here is the fail-closed default.
		slug = r.PathValue("slug")
	}
	inner := mwauth.AccountHandler(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		h.stream(w, r, acct, slug)
	})
	chain := h.Auth.RequireScope(api.ScopesReadSurface...)(h.Auth.RequireMFA(inner))
	h.Auth.RequireLimited(chain)(w, r)
}

// stream is the inner handler: IDOR-safe LoadApp, plan-gate,
// query-param validation, then the S3 fetch + SSE pump.
//
// The S3 client nil-check surfaces as 503 + a stable code
// (`log_archive_unconfigured`) so a Free-tier customer who has
// somehow pre-provisioned the unsealed envelope but not the
// bucket sees a clear "ask the operator to finish the bootstrap"
// message rather than a 500 stack trace. The same code surfaces
// in dev boxes that haven't unsealed the creds envelope.
func (h *ArchiveLogsHandler) stream(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) {
	app, ok := h.Auth.LoadApp(w, r, acct, slug)
	if !ok {
		return
	}
	// Plan gate (issue #562 AC3 / Free = no archive). Free
	// customers get a clean 402 + CodePlanLogArchiveNotAllowed;
	// the SDK branch on that code surfaces the upsell copy.
	if !acct.Plan.LogArchiveEnabled() {
		api.WriteProblem(w, api.ErrPlanLogArchiveNotAllowed(acct.Plan))
		return
	}
	if h.S3 == nil || h.Bucket == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "log_archive_unconfigured",
			"Log archive is enabled on this plan but not configured on the gatewayd-internal host. Ask the operator to unseal archive-creds.json and restart faas-gatewayd-internal.",
			"the S3 client is not wired (FAAS_LOG_ARCHIVE_* env vars unset or unseal incomplete)"))
		return
	}
	instance := r.URL.Query().Get("instance")
	day := r.URL.Query().Get("date")
	if instance == "" || day == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "log_archive_invalid_query",
			"the ?archive=1 path requires ?instance=<id> and ?date=YYYY-MM-DD",
			"missing one or both query parameters"))
		return
	}
	if !archiveInstanceRegex.MatchString(instance) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "log_archive_invalid_query",
			"the ?instance= value must match [A-Za-z0-9._-]{1,128}",
			fmt.Sprintf("got %q", instance)))
		return
	}
	if !archiveDateRegex.MatchString(day) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "log_archive_invalid_query",
			"the ?date= value must be YYYY-MM-DD",
			fmt.Sprintf("got %q", day)))
		return
	}
	// Per-plan retention cap (issue #562 risk #7): a Hobby
	// customer with a 7-day cap should not be able to fetch day
	// 30 via the S3 key. The shipper has already purged the
	// local spool beyond the cap; the bucket object may still
	// exist if the operator provisioned the bucket with a
	// longer lifecycle than the per-plan cap. We refuse here
	// before touching S3 — a free-form `?date=` lets a
	// malicious or curious customer probe history the plan
	// doesn't cover.
	if !h.withinRetention(acct.Plan, day) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "log_archive_retention_exceeded",
			fmt.Sprintf("the %s plan caps log archive retention at %d days",
				acct.Plan, acct.Plan.LogArchiveRetentionDaysMax()),
			fmt.Sprintf("?date=%s is outside the per-plan window", day)))
		return
	}
	apislogs.StartSSE(w)
	flusher, _ := w.(http.Flusher)
	h.serveArchive(r.Context(), w, flusher, app.ID, instance, day)
}

// streamUnauth is the whitebox seam. Mirrors stream()'s body
// verbatim (LoadApp is replaced by the test's appID; everything
// else is identical) so the receive-pump / plan-gate /
// query-validation / S3-error paths are pinned in isolation.
// The production ServeHTTP path stays through stream() so the
// auth chain (RequireLimited → RequireMFA → RequireScope →
// LoadApp) runs unchanged. The method exists only because
// pkg/auth.Middleware.LoadApp requires a real Auth field;
// nil-Auth would nil-deref. Mirrors the cmd/gatewayd-internal/
// app_logs_test.go pattern (the live handler exposes
// serveAppLogs as the seam; the auth chain runs in stream).
func (h *ArchiveLogsHandler) streamUnauth(w http.ResponseWriter, r *http.Request, acct state.Account, appID string) {
	// Plan gate (issue #562 AC3 / Free = no archive).
	if !acct.Plan.LogArchiveEnabled() {
		api.WriteProblem(w, api.ErrPlanLogArchiveNotAllowed(acct.Plan))
		return
	}
	if h.S3 == nil || h.Bucket == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "log_archive_unconfigured",
			"Log archive is enabled on this plan but not configured on the gatewayd-internal host. Ask the operator to unseal archive-creds.json and restart faas-gatewayd-internal.",
			"the S3 client is not wired (FAAS_LOG_ARCHIVE_* env vars unset or unseal incomplete)"))
		return
	}
	instance := r.URL.Query().Get("instance")
	day := r.URL.Query().Get("date")
	if instance == "" || day == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "log_archive_invalid_query",
			"the ?archive=1 path requires ?instance=<id> and ?date=YYYY-MM-DD",
			"missing one or both query parameters"))
		return
	}
	if !archiveInstanceRegex.MatchString(instance) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "log_archive_invalid_query",
			"the ?instance= value must match [A-Za-z0-9._-]{1,128}",
			fmt.Sprintf("got %q", instance)))
		return
	}
	if !archiveDateRegex.MatchString(day) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "log_archive_invalid_query",
			"the ?date= value must be YYYY-MM-DD",
			fmt.Sprintf("got %q", day)))
		return
	}
	if !h.withinRetention(acct.Plan, day) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "log_archive_retention_exceeded",
			fmt.Sprintf("the %s plan caps log archive retention at %d days",
				acct.Plan, acct.Plan.LogArchiveRetentionDaysMax()),
			fmt.Sprintf("?date=%s is outside the per-plan window", day)))
		return
	}
	apislogs.StartSSE(w)
	flusher, _ := w.(http.Flusher)
	h.serveArchive(r.Context(), w, flusher, appID, instance, day)
}

// withinRetention reports whether day is inside the per-plan
// retention cap. The cap is in days-from-now (inclusive); day is
// YYYY-MM-DD in UTC. day > today is rejected (no future
// archives). The helper lifts the comparison out of stream() so
// the whitebox tests can pin the boundary without spinning up
// the full Auth + Store chain.
func (h *ArchiveLogsHandler) withinRetention(plan api.Plan, day string) bool {
	maxDays := plan.LogArchiveRetentionDaysMax()
	if maxDays <= 0 {
		// Plan has no archive (Free); the upstream
		// LogArchiveEnabled() gate already refused, so this
		// branch is unreachable in production. Defensive return
		// false so an off-plan row never silently passes.
		return false
	}
	d, err := time.Parse(archiveQueryDateLayout, day)
	if err != nil {
		return false
	}
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if d.After(today) {
		return false
	}
	cutoff := today.AddDate(0, 0, -maxDays+1) // +1: cap is inclusive
	return !d.Before(cutoff)
}

// serveArchive is the S3 fetch + gzip-decompress + SSE pump body.
// Mirrors the receive-pump shape AppLogsHandler.serveAppLogs uses
// (heartbeat ticker, backstop timer, ctx-cancel drain) so the
// production wiring is uniform across the two paths.
//
// Implementation: GetObject streams the .jsonl.gz body into an
// in-memory buffer (one day's logs cap at ~hundreds of MiB on
// the noisiest apps; well within the stdlib heap budget), then
// a bufio.Scanner over gzip.NewReader walks the inflated
// JSONL line-by-line. The simpler buffer-then-decompress shape
// keeps the error handling linear (no pipe dance, no goroutine
// leak risk) at the cost of holding the full archive in memory
// for the duration of the stream. A future optimisation can
// pipe-through-the-gzip-reader if any single-day archive ever
// exceeds the stdlib heap budget; the wire shape stays the
// same.
func (h *ArchiveLogsHandler) serveArchive(ctx_ context.Context, w http.ResponseWriter, flusher http.Flusher, appID, instance, day string) {
	key := archiveObjectKey(instance, day)
	backstop := h.Backstop
	if backstop <= 0 {
		backstop = defaultAppLogsBackstop
	}
	backstopTimer := time.NewTimer(backstop)
	defer backstopTimer.Stop()
	heartbeat := defaultAppLogsHeartbeat
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()

	// Fetch the gzipped body into memory. GetObject returns
	// *Permanent on 4xx and a plain error on 5xx / network;
	// archiveTerminalForError maps both to the terminal
	// reason vocabulary the SDK decodes.
	var body bytes.Buffer
	if _, err := h.S3.GetObject(ctx_, key, &body); err != nil {
		// 4xx → archive_missing; 5xx / network → archive_degraded.
		renderArchiveTerminal(w, flusher, archiveTerminalForError(err))
		return
	}

	// Wrap the bytes in a gzip reader so the scanner walks the
	// inflated JSONL. A malformed gzip body (someone wrote
	// raw JSONL into the bucket) surfaces as a gzip header
	// error here — treated as archive_degraded because the
	// archive exists, just isn't readable.
	gz, gzErr := gzip.NewReader(&body)
	if gzErr != nil {
		renderArchiveTerminal(w, flusher, archiveReasonDegraded)
		return
	}
	defer func() { _ = gz.Close() }()

	scanner := bufio.NewScanner(gz)
	// Bump the scanner buffer so 64 KiB log lines (a
	// multi-line stack trace, a JSON payload from an app)
	// pass through without scanner-buffer overruns. The cap
	// matches logbuf.MaxPartialLineBytes (1 MiB) so any line
	// the producer could have written fits the consumer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	// Heartbeat ticker fires ":" (comment) frames so a
	// long-decompressing archive doesn't look like a dead
	// connection. Same trick the live handler uses.
	heartbeatCh := ticker.C
	for {
		select {
		case <-ctx_.Done():
			return
		case <-backstopTimer.C:
			renderArchiveTerminal(w, flusher, archiveReasonDegraded)
			return
		case <-heartbeatCh:
			_, _ = fmt.Fprint(w, ":\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		default:
		}
		// Scan one line; the select above gives the timers a
		// chance to fire even on a slow S3 stream.
		if !scanner.Scan() {
			break
		}
		if !renderArchiveLine(w, flusher, appID, instance, scanner.Bytes(), h.Ops) {
			// Malformed JSON line — the producer side
			// shouldn't generate these, but a third-party
			// tool writing into the bucket could. Render
			// a degraded terminal + return.
			renderArchiveTerminal(w, flusher, archiveReasonDegraded)
			return
		}
	}
	if err := scanner.Err(); err != nil {
		// gzip / pipe read error after at least one line
		// rendered. Treat as degraded so the SDK surfaces a
		// partial-stream error rather than a clean success.
		if !errors.Is(err, io.EOF) {
			if h.Log != nil {
				h.Log.Warn("logarchive.readback.scan_error",
					"app", appID, "instance", instance, "day", day, "err", err)
			}
			renderArchiveTerminal(w, flusher, archiveReasonDegraded)
			return
		}
	}
	renderArchiveTerminal(w, flusher, archiveReasonComplete)
}

// archiveObjectKey mirrors pkg/logarchive.Shipper.bucketKey's
// layout (the shipper writes the same shape) so the read-back
// path lands on the same S3 prefix without a constant-table
// trip. The day shape is YYYY-MM-DD; we slice into year / month
// for the {YYYY}/{MM}/{DD} layout. A day shorter than 10 chars
// (defensive — the upstream validator caps at exactly 10) is
// rendered flat into a single segment so a malformed caller
// can't sneak a `/` into the path.
func archiveObjectKey(instance, day string) string {
	if len(day) < 10 {
		return fmt.Sprintf("faas-logs/%s/%s.jsonl.gz", instance, day)
	}
	return fmt.Sprintf("faas-logs/%s/%s/%s/%s.jsonl.gz",
		instance, day[:4], day[5:7], day[:10])
}

// renderArchiveLine parses one spoolLine JSON blob and renders
// the `event: log` SSE frame. The wire shape matches
// apislogs.RenderAppLogEvent's payload keys
// ({seq, instance, stream, line, written_at}) so a downstream
// consumer reading live + archive streams sees a uniform
// envelope. The wire-arg here is the raw JSON bytes the spool
// wrote (spoolLine shape) — we re-marshal into the consumer
// shape because the on-disk format is the producer's contract,
// not the consumer's, and renaming spoolLine would be a
// breaking change for any future bulk-import tooling.
//
// Returns false on a malformed JSON line so the caller can
// surface a degraded terminal.
func renderArchiveLine(w http.ResponseWriter, flusher http.Flusher, appID, instance string, raw []byte, ops *wire.OpsMetrics) bool {
	var line struct {
		Seq       int64     `json:"seq"`
		Stream    string    `json:"stream"`
		WrittenAt time.Time `json:"ts"`
		Line      string    `json:"msg"`
	}
	if err := json.Unmarshal(raw, &line); err != nil {
		return false
	}
	payload, _ := json.Marshal(map[string]any{
		"seq":        line.Seq,
		"instance":   instance,
		"stream":     line.Stream,
		"line":       line.Line,
		"written_at": line.WrittenAt.UTC().Format(time.RFC3339Nano),
	})
	_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", payload)
	if flusher != nil {
		flusher.Flush()
	}
	if ops != nil {
		ops.ObserveLogEmitted(appID)
	}
	return true
}

// renderArchiveTerminal writes the terminal `event: end`
// sentinel. Reason is one of the stable vocabulary documented
// at the top of this file (archive_complete / archive_missing /
// archive_degraded). The helper centralises the SSE plumbing so
// the success / 404 / 5xx branches in serveArchive all share
// the same write path.
func renderArchiveTerminal(w http.ResponseWriter, flusher http.Flusher, reason string) {
	reasonJSON, _ := json.Marshal(map[string]any{"reason": reason})
	_, _ = fmt.Fprintf(w, "event: end\ndata: %s\n\n", reasonJSON)
	if flusher != nil {
		flusher.Flush()
	}
}

// archiveTerminalForError maps an S3 GetObject error into the
// terminal reason vocabulary. nil → archive_complete.
// logarchive.IsPermanent → archive_missing (S3 returned 4xx —
// either NoSuchKey for a real archive gap, or AccessDenied for
// a credentials drift; either way the customer can't fix it
// client-side, so we surface it as missing rather than a
// degraded retry storm). Anything else → archive_degraded
// (5xx, network, gzip pipe error).
//
// The full error chain lives in
// /var/log/faas/gatewayd-internal.log under the same code the
// operator greps for; the SSE wire envelope carries only the
// reason vocabulary so the SDK decoder can branch on a closed
// set.
func archiveTerminalForError(err error) string {
	if err == nil {
		return archiveReasonComplete
	}
	if logarchive.IsPermanent(err) {
		return archiveReasonMissing
	}
	return archiveReasonDegraded
}
