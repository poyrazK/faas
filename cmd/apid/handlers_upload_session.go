// cmd/apid/handlers_upload_session.go — resumable upload
// protocol (issue #1182 §P1 packaging follow-up, PR-1 of 3).
//
// Wire shape:
//
//	POST   /v1/uploads                → handleStartUpload
//	PATCH  /v1/uploads/{id}           → handleAppendUpload
//	POST   /v1/uploads/{id}/commit    → handleCommitUpload
//	DELETE /v1/uploads/{id}           → handleCancelUpload
//
// Why this is separate from handlers_source_tarball.go: the
// single-shot path accepts a complete multipart upload in one
// request; the resumable path decouples session lifetime from
// the request lifetime so a network blip mid-upload doesn't
// lose already-shipped bytes. The two paths share only
// validateTarballShape + apidsource.Enqueue at the commit
// step. The legacy endpoint stays active in PR-1; PR-2 wires
// the CLI to this surface; PR-3 deprecates the legacy
// endpoint with RFC 8594 Sunset headers.
//
// `.part` file lifecycle (spoolRoot at cmd/apid/deploy_inputs.go:37):
//
//	start   → os.Create + os.Truncate(total_size), .part
//	append  → f.WriteAt(buf, offset), no rename
//	commit  → builderd reads SourcePath via hashFile
//	          (pkg/builderd/builderd.go:407); the .part file is
//	          LEFT IN PLACE for builderd to consume (removing
//	          it races the read). The reaper
//	          (cmd/apid/upload_session_reaper.go) sweeps status='open'
//	          expired rows and terminal rows whose 1h builderd grace
//	          period has elapsed.
//	cancel  → os.Remove(.part) AFTER the row's status flips to
//	          cancelled.
//
// Metric wiring uses the established SetOpsMetrics pattern
// (per user MEMORY `wire OpsMetrics package-setter pattern`):
// pkg/wire/metrics.go exports OpsMetrics.UploadSessionCreatedTotal
// etc., and cmd/apid/main.go calls SetOpsMetrics from the
// server boot. Until then the package-level noop default is
// used so the handler compiles standalone in tests.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// uploadSessionLocks serializes all state/file transitions per
// upload_id. Without this guard, a commit can observe the CAS from
// the final PATCH before that PATCH has written the corresponding
// bytes, or a cancel can remove the .part file while a PATCH is
// still writing it. It also serializes the Enqueue + dedupe-row-
// insert + status-flip commit critical section.
//
// In-process only — apid is a single replica per CLAUDE.md,
// so a package-level sync.Map is sufficient. Cross-replica
// concurrency would need pg_advisory_xact_lock (deferred).
var uploadSessionLocks sync.Map // map[string]*sync.Mutex

// acquireUploadSessionLock returns a per-upload_id mutex that the
// caller MUST Unlock after the critical section exits.
func acquireUploadSessionLock(uploadID string) *sync.Mutex {
	if v, ok := uploadSessionLocks.Load(uploadID); ok {
		return v.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := uploadSessionLocks.LoadOrStore(uploadID, mu)
	return actual.(*sync.Mutex)
}

// startUploadRequest is the JSON body POST /v1/uploads accepts.
// total_size is required; sha256_hex is optional (build
// provenance audit row records it, but server does not
// re-verify — ADR-115 trust boundary).
type startUploadRequest struct {
	AppSlug       string                   `json:"app_slug"`
	TotalSize     int64                    `json:"total_size"`
	Sha256Hex     string                   `json:"sha256_hex,omitempty"`
	DeployOptions *api.UploadDeployOptions `json:"deploy_options,omitempty"`
}

// startUploadResponse is the JSON body POST /v1/uploads returns.
// chunk_size is server-decided (8 MiB default; 16 MiB for Scale).
type startUploadResponse struct {
	UploadID  string `json:"upload_id"`
	ChunkSize int32  `json:"chunk_size"`
	TotalSize int64  `json:"total_size"`
	ExpiresAt string `json:"expires_at"`
}

const (
	// uploadChunkSizeDefault is the default chunk_size for
	// non-Scale plans. Matches the §14 metal acceptance gate's
	// minimum size for safe WriteAt performance under tmpfs.
	uploadChunkSizeDefault int32 = 8 * 1024 * 1024 // 8 MiB
	// uploadChunkSizeScalePlan doubles the chunk size for the
	// Scale tier to amortize RTT over larger tarballs.
	uploadChunkSizeScalePlan int32 = 16 * 1024 * 1024 // 16 MiB
	// uploadSessionOpenCap is the per-(account_id, app_slug)
	// cap on concurrent open upload sessions. 5 bounds spool
	// exposure: a misbehaving CLI retrying session creation can
	// leak at most 5 × SourceTarballMaxMB bytes before the cap
	// trips.
	uploadSessionOpenCap = 5
	// uploadSessionSpoolBudgetMultiplier caps the per-account
	// open-spool budget at N × SourceTarballMaxMB. 4× is enough
	// for a Free account to have 4 × 100 MB = 400 MB in flight
	// across 4 apps in parallel without tripping the budget.
	uploadSessionSpoolBudgetMultiplier = 4
)

// handleStartUpload is POST /v1/uploads. Validates plan cap,
// per-account open-session cap, per-account open-spool budget,
// pre-allocates the .part file, and returns upload_id +
// chunk_size.
func (s *server) handleStartUpload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req startUploadRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request body", err.Error()))
		return
	}
	if req.AppSlug == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing app_slug", "app_slug is required"))
		return
	}
	if req.TotalSize <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid total_size", "total_size must be > 0"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	maxBytes := int64(limits.SourceTarballMaxMB) * 1024 * 1024
	if req.TotalSize > maxBytes {
		api.WriteProblem(w, api.ErrSourceTooLarge(limits, req.TotalSize))
		return
	}
	app, err := s.store.AppBySlug(r.Context(), req.AppSlug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such app")
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"App lookup failed", err.Error()))
		return
	}
	if app.AccountID != acct.ID {
		// Do not allow a session to be opened for another account's
		// slug. Besides avoiding a later commit failure, this keeps
		// the cap/budget queries from becoming an app-existence oracle.
		s.notFound(w, "no such app")
		return
	}

	acctUUID := pgtypeFromUUIDString(acct.ID)
	// Keep the cap and budget checks together with the INSERT. The
	// checks are intentionally handler-side because plan limits are
	// dynamic; serializing starts for one account closes the in-process
	// check-then-insert race across different apps. A multi-replica
	// deployment still needs a database-backed admission primitive.
	startMu := acquireUploadSessionLock("account:" + acct.ID)
	startMu.Lock()
	defer startMu.Unlock()

	openCount, err := s.store.CountOpenUploadSessionsByAccountApp(r.Context(), sqlc.CountOpenUploadSessionsByAccountAppParams{
		Column1: acctUUID,
		AppSlug: req.AppSlug,
	})
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Count failed", err.Error()))
		return
	}
	if openCount >= uploadSessionOpenCap {
		api.WriteProblem(w, api.ErrUploadSessionTooMany(
			fmt.Sprintf("Account has %d open upload sessions on app %q (cap %d). Cancel one before opening another.", openCount, req.AppSlug, uploadSessionOpenCap),
			maxBytes*uploadSessionSpoolBudgetMultiplier, int64(openCount)*maxBytes))
		return
	}

	openBytes, err := s.store.SumOpenUploadSessionBytesByAccount(r.Context(), acctUUID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Sum failed", err.Error()))
		return
	}
	budgetBytes := maxBytes * uploadSessionSpoolBudgetMultiplier
	if openBytes+req.TotalSize > budgetBytes {
		api.WriteProblem(w, api.ErrUploadSessionTooMany(
			fmt.Sprintf("Account has %d bytes in open upload sessions (budget %d). Commit or cancel one before opening another.", openBytes, budgetBytes),
			budgetBytes, openBytes+req.TotalSize))
		return
	}

	chunkSize := uploadChunkSizeDefault
	if acct.Plan == api.PlanScale {
		chunkSize = uploadChunkSizeScalePlan
	}

	uploadID := randomToken(12)
	partPath := spoolRoot() + "/" + uploadID + ".part"
	if err := os.MkdirAll(spoolRoot(), 0o770); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Spool mkdir failed", err.Error()))
		return
	}
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o660)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Spool create failed", err.Error()))
		return
	}
	if err := f.Truncate(req.TotalSize); err != nil {
		_ = f.Close()
		_ = os.Remove(partPath)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Spool truncate failed", err.Error()))
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(partPath)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Spool close failed", err.Error()))
		return
	}

	deployOptions := []byte("{}")
	if req.DeployOptions != nil {
		deployOptions, err = json.Marshal(req.DeployOptions)
		if err != nil || len(deployOptions) > 1<<20 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid deploy_options", "deploy_options must be valid JSON smaller than 1 MiB"))
			return
		}
	}
	row, err := s.store.CreateUploadSession(r.Context(), sqlc.CreateUploadSessionParams{
		ID:            uploadID,
		AccountID:     acctUUID,
		AppSlug:       req.AppSlug,
		TotalSize:     req.TotalSize,
		ChunkSize:     chunkSize,
		Sha256Hex:     pgtype.Text{String: req.Sha256Hex, Valid: req.Sha256Hex != ""},
		PartPath:      partPath,
		DeployOptions: deployOptions,
	})
	if err != nil {
		_ = os.Remove(partPath)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Create session failed", err.Error()))
		return
	}

	uploadSessionCreatedTotal().WithLabelValues(string(acct.Plan)).Inc()

	s.log.Info("upload session opened",
		"upload_id", uploadID, "app_slug", req.AppSlug,
		"total_size", req.TotalSize, "chunk_size", chunkSize,
		"plan", acct.Plan, "account", acct.ID)

	writeJSON(w, http.StatusCreated, startUploadResponse{
		UploadID:  row.ID,
		ChunkSize: row.ChunkSize,
		TotalSize: row.TotalSize,
		ExpiresAt: row.ExpiresAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// handleGetUpload is GET /v1/uploads/{id}. It deliberately returns only
// resumable protocol state, never the spool path or persisted deployment
// metadata. This is the discovery seam for clients resuming after restart.
func (s *server) handleGetUpload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	uploadID := r.PathValue("id")
	row, err := s.store.GetUploadSession(r.Context(), uploadID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Get session failed", err.Error()))
		return
	}
	if row.AccountID != pgtypeFromUUIDString(acct.ID) {
		api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
		return
	}
	var deploymentID *string
	if row.DeploymentID.Valid {
		value := row.DeploymentID.String
		deploymentID = &value
	}
	writeJSON(w, http.StatusOK, api.UploadSessionResponse{
		UploadID: row.ID, AppSlug: row.AppSlug, ChunkSize: row.ChunkSize,
		TotalSize: row.TotalSize, ReceivedBytes: row.ReceivedBytes,
		Status: row.Status, ExpiresAt: row.ExpiresAt.Time.UTC().Format(time.RFC3339),
		DeploymentID: deploymentID,
	})
}

// handleAppendUpload is PATCH /v1/uploads/{id}. Reads Upload-Offset
// + body bytes, runs the atomic CAS in AppendUploadBytes, then
// WriteAt's the bytes onto the .part file.
func (s *server) handleAppendUpload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	uploadID := r.PathValue("id")
	offsetHeader := r.Header.Get("Upload-Offset")
	if offsetHeader == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing Upload-Offset", "Upload-Offset header is required"))
		return
	}
	clientOffset, err := strconv.ParseInt(offsetHeader, 10, 64)
	if err != nil || clientOffset < 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad Upload-Offset", "Upload-Offset must be a non-negative integer"))
		return
	}

	row, err := s.store.GetUploadSession(r.Context(), uploadID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Get session failed", err.Error()))
		return
	}
	acctUUID := pgtypeFromUUIDString(acct.ID)
	if row.AccountID != acctUUID {
		// Defense in depth: even though the auth chain enforces
		// account scoping, the store's CAS rejects the write via
		// the (id, account_id) predicate. Returning 404 (not 403)
		// avoids leaking that the upload_id exists in another
		// account's namespace.
		api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
		return
	}
	if row.Status != "open" {
		s.writeUploadSessionTerminalProblem(r.Context(), w, row)
		return
	}
	if uploadSessionExpired(row) {
		api.WriteProblem(w, api.ErrUploadSessionExpired(uploadID))
		return
	}
	if clientOffset != row.ReceivedBytes {
		api.WriteProblem(w, api.ErrUploadSessionOffsetConflict(uploadID, clientOffset, row.ReceivedBytes))
		return
	}

	// Read the chunk bytes. Cap at row.ChunkSize + 1 so an
	// oversized chunk trips the limit on the first io.ReadAll.
	body := http.MaxBytesReader(w, r.Body, int64(row.ChunkSize)+1)
	chunk, err := io.ReadAll(body)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Body read failed", err.Error()))
		return
	}
	if int64(len(chunk)) > int64(row.ChunkSize) {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeValidation,
			"Chunk too large", fmt.Sprintf("chunk size %d exceeds server chunk_size %d", len(chunk), row.ChunkSize)))
		return
	}

	// The database CAS protects the offset, while this lock protects
	// the paired row/file transition. Re-read after acquiring it: the
	// first row read can race a commit, cancel, or the reaper while the
	// request body is being received.
	mu := acquireUploadSessionLock(uploadID)
	mu.Lock()
	defer mu.Unlock()
	row, err = s.store.GetUploadSession(r.Context(), uploadID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Get session failed", err.Error()))
		return
	}
	if row.AccountID != acctUUID {
		api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
		return
	}
	if row.Status != "open" {
		s.writeUploadSessionTerminalProblem(r.Context(), w, row)
		return
	}
	if uploadSessionExpired(row) {
		api.WriteProblem(w, api.ErrUploadSessionExpired(uploadID))
		return
	}
	if clientOffset != row.ReceivedBytes {
		api.WriteProblem(w, api.ErrUploadSessionOffsetConflict(uploadID, clientOffset, row.ReceivedBytes))
		return
	}

	// Atomic CAS: server received_bytes goes from clientOffset
	// to clientOffset + len(chunk). A racing PATCH that already
	// advanced fails with ErrConflict (sql.ErrNoRows mapped in
	// PgStore).
	newReceived := clientOffset + int64(len(chunk))
	appendRow, err := s.store.AppendUploadBytes(r.Context(), sqlc.AppendUploadBytesParams{
		ID:              uploadID,
		ReceivedBytes:   newReceived,
		ReceivedBytes_2: clientOffset,
	})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			current, getErr := s.store.GetUploadSession(r.Context(), uploadID)
			if getErr != nil {
				api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeUploadSessionOffsetConflict,
					"Upload session offset conflict",
					"Another PATCH already advanced the offset; retry with the server's current offset."))
				return
			}
			api.WriteProblem(w, api.ErrUploadSessionOffsetConflict(uploadID, clientOffset, current.ReceivedBytes))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Append failed", err.Error()))
		return
	}

	// CAS won — WriteAt the bytes to the .part file. The file
	// was pre-allocated to total_size, so the seek is a no-op.
	f, err := os.OpenFile(row.PartPath, os.O_WRONLY, 0o660)
	if err != nil {
		// CAS already advanced the row; leave the row in a
		// "row ahead of file" state. Reaper sweep
		// (status='open' + expires_at < now()) cleans within
		// 24h. Customer retries from a fresh session.
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Spool open failed", err.Error()))
		return
	}
	if _, err := f.WriteAt(chunk, clientOffset); err != nil {
		_ = f.Close()
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Spool write failed", err.Error()))
		return
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Spool fsync failed", err.Error()))
		return
	}
	if err := f.Close(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Spool close failed", err.Error()))
		return
	}

	w.Header().Set("Upload-Offset", strconv.FormatInt(appendRow.ReceivedBytes, 10))
	w.WriteHeader(http.StatusOK)
}

// handleCommitUpload is POST /v1/uploads/{id}/commit. Validates
// tarball shape over the .part file, enqueues the build, records
// the dedupe row, and marks the session committed.
func (s *server) handleCommitUpload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	uploadID := r.PathValue("id")
	// Serialize per-upload_id upload/file critical sections to
	// prevent two concurrent commit retries both calling
	// apidsource.Enqueue and leaving an orphan deployment.
	// In-process only (single-replica per CLAUDE.md); see
	// acquireUploadSessionLock above for the deferral note.
	mu := acquireUploadSessionLock(uploadID)
	mu.Lock()
	defer mu.Unlock()
	row, err := s.store.GetUploadSession(r.Context(), uploadID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Commit retry after the row's reaper sweep: surface
			// the upload_commit_outcomes dedupe row if present.
			if outcome, getErr := s.store.GetUploadCommitOutcome(r.Context(), uploadID); getErr == nil {
				api.WriteProblem(w, api.ErrUploadSessionAlreadyCommitted(uploadID, outcome.DeploymentID))
				return
			}
			api.WriteProblem(w, api.ErrUploadSessionExpired(uploadID))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Get session failed", err.Error()))
		return
	}
	if row.AccountID != pgtypeFromUUIDString(acct.ID) {
		api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
		return
	}
	if outcome, outcomeErr := s.store.GetUploadCommitOutcome(r.Context(), uploadID); outcomeErr == nil {
		api.WriteProblem(w, api.ErrUploadSessionAlreadyCommitted(uploadID, outcome.DeploymentID))
		return
	} else if !errors.Is(outcomeErr, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Get commit outcome failed", outcomeErr.Error()))
		return
	}
	if row.Status != "open" {
		s.writeUploadSessionTerminalProblem(r.Context(), w, row)
		return
	}
	if uploadSessionExpired(row) {
		api.WriteProblem(w, api.ErrUploadSessionExpired(uploadID))
		return
	}
	if row.ReceivedBytes != row.TotalSize {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Incomplete upload",
			fmt.Sprintf("Session received %d of %d bytes; PATCH the remaining bytes before committing.", row.ReceivedBytes, row.TotalSize)))
		return
	}

	app, err := s.store.AppBySlug(r.Context(), row.AppSlug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such app")
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"App lookup failed", err.Error()))
		return
	}
	// Defense in depth: the auth chain + upload_sessions row's
	// account_id together establish the (acct, slug) tenancy,
	// but verify the resolved app is owned by this account.
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
		return
	}
	var opts api.UploadDeployOptions
	if len(row.DeployOptions) > 0 && string(row.DeployOptions) != "{}" {
		if err := json.Unmarshal(row.DeployOptions, &opts); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
				"Invalid persisted deploy options", err.Error()))
			return
		}
	}
	if opts.SourceRoot != "" {
		root, rootErr := sourcecontext.StorageRoot(opts.SourceRoot)
		if rootErr != nil {
			api.WriteProblem(w, api.ErrSourceInvalid("invalid source_root: "+rootErr.Error()))
			return
		}
		opts.SourceRoot = root
		present, rootErr := archiveHasSourceRoot(row.PartPath, opts.SourceRoot)
		if rootErr != nil || !present {
			if rootErr != nil {
				api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", rootErr.Error()))
			} else {
				api.WriteProblem(w, api.ErrSourceInvalid(fmt.Sprintf("source_root %q is not present in the source archive", opts.SourceRoot)))
			}
			return
		}
	}
	if app.Type == state.AppTypeFunction {
		if opts.Dockerfile {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Dockerfile functions unsupported", "function deploys use the platform runtime scaffold; remove dockerfile=true"))
			return
		}
		if opts.Runtime != "" && opts.Runtime != app.Runtime {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Runtime mismatch", "function deploys must match the app's runtime"))
			return
		}
		if opts.Handler == "" {
			api.WriteProblem(w, api.ErrHandlerMissing())
			return
		}
	}
	ann := annotationForm{Reason: opts.Reason, Tag: opts.Tag, DeployedBy: opts.DeployedBy, PRNumber: opts.PRNumber}
	if prob := validateAnnotationForm(ann); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateWorkflowDefinitionsAgainstPlan(opts.Workflows, acct.Plan); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	if prob := validateTarballShape(row.PartPath); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := scanForStatefulShapeAtRoot(row.PartPath, opts.Dockerfile, opts.SourceRoot); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	limits := api.MustLimitsFor(acct.Plan)

	// apidsource.Enqueue never deletes the staged SourcePath —
	// builderd reads it. We leave the .part in place; the reaper
	// sweeps committed/cancelled/expired .part files older than 1h
	// to bound the spool leak while giving builderd time to consume.
	kind := state.DeploymentKindTarball
	if opts.Dockerfile {
		kind = state.DeploymentKindDockerfile
	}
	res, err := apidsource.Enqueue(r.Context(), s.store, s.notif, apidsource.EnqueueParams{
		AppID:            app.ID,
		Kind:             kind,
		SourcePath:       row.PartPath,
		SourceBytes:      row.ReceivedBytes,
		SourceRoot:       opts.SourceRoot,
		Handler:          opts.Handler,
		SourceURL:        "local-tar://upload-session/" + uploadID,
		Source:           "upload-session:" + uploadID,
		LogSpool:         spoolRoot(),
		Log:              s.log,
		ActorUserID:      acct.ID,
		ActorVia:         routeKindForRequest(r),
		ActorFromIP:      middleware.ClientIP(r),
		ActorPusherLogin: "",
		Reason:           opts.Reason,
		Tag:              opts.Tag,
		DeployedBy:       opts.DeployedBy,
		PRNumber:         opts.PRNumber,
		Workflows:        marshalWorkflowDefinitions(opts.Workflows),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
		return
	}

	// Record the dedupe outcome before flipping the session state so a
	// retry after a response loss can recover the original deployment.
	// A conflict is only safe to treat as a retry when the canonical
	// outcome can actually be read; otherwise returning an error avoids
	// marking the session with an outcome that belongs to another build.
	if _, err := s.store.RecordUploadCommitOutcome(r.Context(), sqlc.RecordUploadCommitOutcomeParams{
		UploadID:     uploadID,
		DeploymentID: res.DeploymentID,
		BuildID:      res.BuildID,
	}); err != nil {
		if errors.Is(err, state.ErrConflict) {
			if outcome, getErr := s.store.GetUploadCommitOutcome(r.Context(), uploadID); getErr == nil {
				api.WriteProblem(w, api.ErrUploadSessionAlreadyCommitted(uploadID, outcome.DeploymentID))
				return
			} else {
				api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
					"Commit outcome conflict without stored outcome", getErr.Error()))
				return
			}
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Record outcome failed", err.Error()))
		return
	}

	committedRow, err := s.store.MarkUploadSessionCommitted(r.Context(), sqlc.MarkUploadSessionCommittedParams{
		ID:           uploadID,
		DeploymentID: pgtype.Text{String: res.DeploymentID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			if outcome, getErr := s.store.GetUploadCommitOutcome(r.Context(), uploadID); getErr == nil {
				api.WriteProblem(w, api.ErrUploadSessionAlreadyCommitted(uploadID, outcome.DeploymentID))
				return
			}
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Mark committed failed", err.Error()))
		return
	}
	_ = committedRow // satisfied for the side effect

	uploadSessionCommittedTotal().WithLabelValues(string(acct.Plan)).Inc()

	s.auditUploadSessionCommitted(r.Context(), acct, app, uploadID, res.DeploymentID, res.BuildID, row.ReceivedBytes, limits)

	// Read the deployment created by this commit, not merely the
	// latest deployment for the app. Another deploy may legitimately
	// arrive between Enqueue and this response.
	d, err := s.store.DeploymentByID(r.Context(), res.DeploymentID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read deployment"))
		return
	}
	writeJSON(w, http.StatusCreated, s.deploymentResponse(d, app))
}

// handleCancelUpload is DELETE /v1/uploads/{id}. Transitions open
// → cancelled and removes the .part file.
func (s *server) handleCancelUpload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	uploadID := r.PathValue("id")
	acctUUID := pgtypeFromUUIDString(acct.ID)
	mu := acquireUploadSessionLock(uploadID)
	mu.Lock()
	defer mu.Unlock()
	row, err := s.store.GetUploadSession(r.Context(), uploadID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Get session failed", err.Error()))
		return
	}
	if row.AccountID != acctUUID {
		api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
		return
	}
	err = s.store.CancelUploadSession(r.Context(), sqlc.CancelUploadSessionParams{
		ID:      uploadID,
		Column2: acctUUID,
	})
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrUploadSessionNotFound(uploadID))
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrUploadSessionAlreadyCancelled(uploadID))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Cancel failed", err.Error()))
		return
	}

	// Best-effort .part removal. ErrNotExist is logged + ignored
	// (the reaper may have deleted it first).
	if row.PartPath != "" {
		if rmErr := removeUploadPart(row.PartPath); rmErr != nil && !os.IsNotExist(rmErr) {
			s.log.Warn("upload session cancel: .part remove failed",
				"upload_id", uploadID, "path", row.PartPath, "err", rmErr)
		} else if clearErr := s.store.ClearUploadSessionPartPath(r.Context(), uploadID); clearErr != nil {
			s.log.Warn("upload session cancel: clear part path failed",
				"upload_id", uploadID, "path", row.PartPath, "err", clearErr)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func uploadSessionExpired(row sqlc.UploadSession) bool {
	return row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(time.Now())
}

func (s *server) writeUploadSessionTerminalProblem(ctx context.Context, w http.ResponseWriter, row sqlc.UploadSession) {
	if row.Status == "committed" {
		if outcome, err := s.store.GetUploadCommitOutcome(ctx, row.ID); err == nil {
			api.WriteProblem(w, api.ErrUploadSessionAlreadyCommitted(row.ID, outcome.DeploymentID))
			return
		}
		api.WriteProblem(w, api.ErrUploadSessionAlreadyCommitted(row.ID, row.DeploymentID.String))
		return
	}
	if row.Status == "expired" {
		api.WriteProblem(w, api.ErrUploadSessionExpired(row.ID))
		return
	}
	api.WriteProblem(w, api.ErrUploadSessionAlreadyCancelled(row.ID))
}

// auditUploadSessionCommitted emits the `upload.session_committed`
// audit row. Mirrors auditLocalTarballDeploy
// (cmd/apid/handlers_source_tarball.go:206-253) but uses the
// wire-stable `upload.session_committed` kind so the audit log
// distinguishes the resumable path from the legacy single-shot.
func (s *server) auditUploadSessionCommitted(
	ctx context.Context,
	acct state.Account,
	app state.App,
	uploadID, deploymentID, buildID string,
	sourceBytes int64,
	limits api.Limits,
) {
	d, dErr := s.store.DeploymentByID(ctx, deploymentID)
	resolvedActor := ""
	if dErr == nil {
		resolvedActor = resolvedActorString(d.DeployedVia, d.DeployedByUserID, d.PusherLogin)
	} else {
		s.log.Warn("auditUploadSessionCommitted: read deployment for actor attribution failed",
			"deployment", deploymentID, "err", dErr)
	}
	data := map[string]any{
		auditKeyAppID:        app.ID,
		auditKeyDeploymentID: deploymentID,
		auditKeyBuildID:      buildID,
		"upload_id":          uploadID,
		auditKeySourceBytes:  sourceBytes,
		auditKeyTrustRoot:    "cli",
	}
	if dErr == nil {
		mergeActorAudit(data, d.DeployedByUserID, d.DeployedVia, d.DeployedFromIP, d.PusherLogin)
	}
	s.audit.EmitAs(ctx, resolvedActor, "upload.session_committed", &acct.ID, data)
	_ = limits // reserved for future ADR-driven per-plan source-tarball bump
}

// uploadSessionCounters holds the Prometheus counter vecs the
// handlers increment. main.go calls SetUploadSessionCounters
// from the server boot to wire the real counters from
// pkg/wire/metrics.go's *OpsMetrics. The default is the noop
// counter so the handler compiles + tests run before main.go's
// wiring runs (mirrors pkg/rootfs/layer.go:33).
type uploadSessionCounters struct {
	CreatedTotal           uploadCounterVec
	CommittedTotal         uploadCounterVec
	ExpiredTotal           uploadCounter
	ReaperRowsDeletedTotal uploadCounter
	ReaperFailedTotal      uploadCounter
}

// uploadCounter is the Prometheus Counter / CounterVec surface
// the handlers use (.Inc()). The {plan} series need
// WithLabelValues(...) returning an uploadCounter; the unlabelled
// reaper counters just need Inc().
type uploadCounter interface{ Inc() }

// promCounterVecAdapter wraps prometheus.CounterVec so its
// WithLabelValues() returns an uploadCounter (matching the
// handler-side interface). Without the adapter, the handler
// needs to know about the *prometheus.CounterVec return type
// and call .Inc() via 2 layers.
type promCounterVecAdapter struct{ v *prometheus.CounterVec }

// WithLabelValues returns the prometheus.Counter for the
// (plan) label series. If v is nil (OpsMetrics not wired),
// returns the noop default — fail-safe closed.
func (a promCounterVecAdapter) WithLabelValues(plan ...string) uploadCounter {
	if a.v == nil {
		return uploadSessionCounterNoop{}
	}
	return a.v.WithLabelValues(plan...)
}

// promCounterAdapter wraps a plain prometheus.Counter so the
// uploadSessionCounters struct's 3 unlabelled slots accept the
// real counter type without forcing the handler to import
// prometheus directly.
type promCounterAdapter struct{ c prometheus.Counter }

func (a promCounterAdapter) Inc() {
	if a.c == nil {
		return
	}
	a.c.Inc()
}

// uploadCounterVec mirrors the prometheus.CounterVec.WithLabelValues
// + .Inc() pattern that the {plan} counters need. The default
// noop returns itself so chained calls are no-ops.
type uploadCounterVec interface {
	WithLabelValues(labels ...string) uploadCounter
}

// uploadSessionCounterNoop is the pre-wire default. WithLabelValues
// returns uploadCounter (interface) so it satisfies
// uploadCounterVec; the returned uploadSessionCounterNoop value
// has Inc() and so satisfies uploadCounter.
type uploadSessionCounterNoop struct{}

func (uploadSessionCounterNoop) Inc() {}
func (uploadSessionCounterNoop) WithLabelValues(_ ...string) uploadCounter {
	return uploadSessionCounterNoop{}
}

var uploadSessionCounterState = uploadSessionCounters{
	CreatedTotal:           uploadSessionCounterNoop{},
	CommittedTotal:         uploadSessionCounterNoop{},
	ExpiredTotal:           uploadSessionCounterNoop{},
	ReaperRowsDeletedTotal: uploadSessionCounterNoop{},
	ReaperFailedTotal:      uploadSessionCounterNoop{},
}

// SetUploadSessionCounters is called by main.go after NewOpsMetrics.
// After this returns, the handler increments hit the real
// Prometheus counters registered on the apid's registry.
func SetUploadSessionCounters(c uploadSessionCounters) { uploadSessionCounterState = c }

func uploadSessionCreatedTotal() uploadCounterVec   { return uploadSessionCounterState.CreatedTotal }
func uploadSessionCommittedTotal() uploadCounterVec { return uploadSessionCounterState.CommittedTotal }
func uploadSessionExpiredTotal() uploadCounter      { return uploadSessionCounterState.ExpiredTotal }
func uploadSessionReaperRowsDeletedTotal() uploadCounter {
	return uploadSessionCounterState.ReaperRowsDeletedTotal
}
func uploadSessionReaperFailedTotal() uploadCounter {
	return uploadSessionCounterState.ReaperFailedTotal
}

// pgtypeFromUUIDString is defined at handlers_app_errors_projection.go:171.
// It converts state.Account.ID (a stringified uuid.UUID) into
// pgtype.UUID. Returns Valid=false on parse failure so the CAS
// predicate returns ErrConflict — fail-closed.
