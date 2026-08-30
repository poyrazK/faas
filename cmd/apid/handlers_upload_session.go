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
//	          it races the read). Future work (out of scope for
//	          PR-1) sweeps status IN (committed, cancelled,
//	          expired) .part files older than 1h to bound the
//	          spool leak. The reaper (cmd/apid/upload_session_reaper.go)
//	          sweeps status='open' expired rows.
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

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// startUploadRequest is the JSON body POST /v1/uploads accepts.
// total_size is required; sha256_hex is optional (build
// provenance audit row records it, but server does not
// re-verify — ADR-115 trust boundary).
type startUploadRequest struct {
	AppSlug   string `json:"app_slug"`
	TotalSize int64  `json:"total_size"`
	Sha256Hex string `json:"sha256_hex,omitempty"`
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

	acctUUID := pgtypeFromUUIDString(acct.ID)

	openCount, err := s.store.CountOpenUploadSessionsByAccountApp(r.Context(), sqlc.CountOpenUploadSessionsByAccountAppParams{
		Column1: acctUUID,
		AppSlug: req.AppSlug,
	})
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
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
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
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
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Spool mkdir failed", err.Error()))
		return
	}
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o660)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Spool create failed", err.Error()))
		return
	}
	if err := f.Truncate(req.TotalSize); err != nil {
		_ = f.Close()
		_ = os.Remove(partPath)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Spool truncate failed", err.Error()))
		return
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(partPath)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Spool close failed", err.Error()))
		return
	}

	row, err := s.store.CreateUploadSession(r.Context(), sqlc.CreateUploadSessionParams{
		ID:        uploadID,
		AccountID: acctUUID,
		AppSlug:   req.AppSlug,
		TotalSize: req.TotalSize,
		ChunkSize: chunkSize,
		Sha256Hex: pgtype.Text{String: req.Sha256Hex, Valid: req.Sha256Hex != ""},
		PartPath:  partPath,
	})
	if err != nil {
		_ = os.Remove(partPath)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
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
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Get session failed", err.Error()))
		return
	}
	if row.Status != "open" {
		if row.Status == "expired" {
			api.WriteProblem(w, api.ErrUploadSessionExpired(uploadID))
		} else {
			api.WriteProblem(w, api.ErrUploadSessionAlreadyCancelled(uploadID))
		}
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
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
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
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Spool open failed", err.Error()))
		return
	}
	if _, err := f.WriteAt(chunk, clientOffset); err != nil {
		_ = f.Close()
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Spool write failed", err.Error()))
		return
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Spool fsync failed", err.Error()))
		return
	}
	if err := f.Close(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
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
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Get session failed", err.Error()))
		return
	}
	if row.Status != "open" {
		if outcome, getErr := s.store.GetUploadCommitOutcome(r.Context(), uploadID); getErr == nil {
			api.WriteProblem(w, api.ErrUploadSessionAlreadyCommitted(uploadID, outcome.DeploymentID))
			return
		}
		api.WriteProblem(w, api.ErrUploadSessionAlreadyCancelled(uploadID))
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
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
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

	if prob := validateTarballShape(row.PartPath); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := scanForStatefulShape(row.PartPath, false); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	limits := api.MustLimitsFor(acct.Plan)

	// apidsource.Enqueue never deletes the staged SourcePath —
	// builderd reads it. We leave the .part in place; future
	// work (out of scope for PR-1) sweeps committed/cancelled/
	// expired .part files older than 1h to bound the spool leak.
	res, err := apidsource.Enqueue(r.Context(), s.store, s.notif, apidsource.EnqueueParams{
		AppID:            app.ID,
		Kind:             state.DeploymentKindTarball,
		SourcePath:       row.PartPath,
		SourceBytes:      row.ReceivedBytes,
		SourceURL:        "local-tar://upload-session/" + uploadID,
		Source:           "upload-session:" + uploadID,
		LogSpool:         spoolRoot(),
		Log:              s.log,
		ActorUserID:      acct.ID,
		ActorVia:         routeKindForRequest(r),
		ActorFromIP:      middleware.ClientIP(r),
		ActorPusherLogin: "",
		DeployedBy:       acct.ID,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
		return
	}

	// Dedupe row first — if MarkUploadSessionCommitted fails
	// because the row is already terminal (a racing commit),
	// the dedupe row's ON CONFLICT DO NOTHING protects us from
	// creating two outcomes for the same upload_id.
	if _, err := s.store.RecordUploadCommitOutcome(r.Context(), sqlc.RecordUploadCommitOutcomeParams{
		UploadID:     uploadID,
		DeploymentID: res.DeploymentID,
		BuildID:      res.BuildID,
	}); err != nil && !errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
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
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Mark committed failed", err.Error()))
		return
	}
	_ = committedRow // satisfied for the side effect

	uploadSessionCommittedTotal().WithLabelValues(string(acct.Plan)).Inc()

	s.auditUploadSessionCommitted(r.Context(), acct, app, uploadID, res.DeploymentID, res.BuildID, row.ReceivedBytes, limits)

	d, err := s.store.LatestDeployment(r.Context(), app.ID)
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
	err := s.store.CancelUploadSession(r.Context(), sqlc.CancelUploadSessionParams{
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
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeValidation,
			"Cancel failed", err.Error()))
		return
	}

	// Best-effort .part removal. ErrNotExist is logged + ignored
	// (the reaper may have deleted it first).
	row, _ := s.store.GetUploadSession(r.Context(), uploadID)
	if row.PartPath != "" {
		if rmErr := os.Remove(row.PartPath); rmErr != nil && !os.IsNotExist(rmErr) {
			s.log.Warn("upload session cancel: .part remove failed",
				"upload_id", uploadID, "path", row.PartPath, "err", rmErr)
		}
	}

	w.WriteHeader(http.StatusNoContent)
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

type uploadCounter interface{ Inc() }

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
func (uploadSessionCounterNoop) WithLabelValues(_ ...string) uploadCounter { return uploadSessionCounterNoop{} }

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

func uploadSessionCreatedTotal() uploadCounterVec { return uploadSessionCounterState.CreatedTotal }
func uploadSessionCommittedTotal() uploadCounterVec { return uploadSessionCounterState.CommittedTotal }
func uploadSessionExpiredTotal() uploadCounter     { return uploadSessionCounterState.ExpiredTotal }
func uploadSessionReaperRowsDeletedTotal() uploadCounter { return uploadSessionCounterState.ReaperRowsDeletedTotal }
func uploadSessionReaperFailedTotal() uploadCounter { return uploadSessionCounterState.ReaperFailedTotal }

// pgtypeFromUUIDString is defined at handlers_app_errors_projection.go:171.
// It converts state.Account.ID (a stringified uuid.UUID) into
// pgtype.UUID. Returns Valid=false on parse failure so the CAS
// predicate returns ErrConflict — fail-closed.