// apid-side gRPC handler for the AppErrors service (ADR-096).
// Wired by registerAppErrorsReceiver in cmd/apid/main.go onto a
// *grpc.Server that runAppErrorsServer owns.
//
// Direction: gatewayd-internal → apid. gatewayd-internal is the
// ONLY caller (CLAUDE.md ownership: apid is the sole writer to
// app_errors / app_error_requests; gatewayd-internal never opens
// a direct Postgres connection for this store).
//
// Wire discipline mirrors cmd/apid/advisory_receiver.go: errors
// map to gRPC codes (InvalidArgument / NotFound / ResourceExhausted
// / Internal), per-record outcomes ride on the response stream,
// transient DB failures do NOT abort the stream.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Outcome strings ride on the IncrementAppErrorResponse stream
// from apid back to gatewayd-internal. The §12 metric
// faas_gateway_app_errors_recorded_total{outcome="..."} keys off
// these literals — they MUST stay aligned with the wire-side
// constants in pkg/wire/metrics.go (goconst-flagged so any drift
// breaks the lint gate).
const (
	outcomeInserted        = "inserted"
	outcomeMerged          = "merged"
	outcomeRedactionFailed = "redaction_failed"
	outcomeRateLimited     = "rate_limited"
	outcomeDBError         = "db_error"
)

// appErrorsStore is the subset of pkg/state.Store the receiver
// needs (ADR-096 / IncrementAppError path). The interface is
// declared here so unit tests can substitute a fake without
// spinning a real Postgres pool.
type appErrorsStore interface {
	IncrementAppError(ctx context.Context, arg sqlc.IncrementAppErrorParams) (bool, error)
	InsertAppErrorRequest(ctx context.Context, arg sqlc.InsertAppErrorRequestParams) error
}

// appErrorsReceiver is the in-package server implementation of
// apidpb.AppErrorsServer. Wired by registerAppErrorsReceiver
// onto a *grpc.Server.
//
// ops is the apid daemon's *wire.OpsMetrics; nil-safe so a unit
// test that doesn't wire metrics keeps building.
//
// enabled is the kill-switch (FAAS_APP_ERRORS_ENABLED). When
// false, IncrementAppError returns codes.Unavailable so the
// gateway stops sending. PR-A ships with this defaulting to
// false; PR-B flips it to true.
type appErrorsReceiver struct {
	apidpb.UnimplementedAppErrorsServer
	store   appErrorsStore
	ops     *wire.OpsMetrics
	enabled bool
}

// newAppErrorsReceiver wires a production receiver.
func newAppErrorsReceiver(store appErrorsStore, ops *wire.OpsMetrics, enabled bool) *appErrorsReceiver {
	return &appErrorsReceiver{store: store, ops: ops, enabled: enabled}
}

// IncrementAppError streams per-record error records from the
// gateway edge and writes each one via the dedupe-merge INSERT
// + the per-request row INSERT. The server commits each record
// inside its own transaction (per-record commit; load-bearing
// for the dedupe-merge semantics — see ADR-096 §3.5).
//
// Outcome ∈ {inserted, merged, redaction_failed, rate_limited,
// db_error}. The first two are success outcomes; the latter
// three are observability signals — the gateway MUST NOT retry
// on them (a retry would double-count).
func (a *appErrorsReceiver) IncrementAppError(stream apidpb.AppErrors_IncrementAppErrorServer) error {
	if !a.enabled {
		// Kill-switch: refuse the stream so the gateway stops
		// sending. Cancelling the context here drops any in-flight
		// records without committing them.
		return status.Error(codes.Unavailable, "app_errors disabled by FAAS_APP_ERRORS_ENABLED")
	}
	for {
		req, err := stream.Recv()
		if err != nil {
			// io.EOF or context cancel: end of stream.
			//nolint:nilerr // io.EOF on stream.Recv is the canonical "client half-closed" signal — returning nil closes the stream cleanly without surfacing the EOF as an error.
			return nil
		}
		out := a.handleOne(stream.Context(), req)
		if err := stream.Send(out); err != nil {
			// Stream is broken; bail. The gateway will observe
			// the drop on its side.
			return err
		}
	}
}

// handleOne processes a single record and returns the per-record
// outcome. Never returns an error — every failure path maps to
// one of the closed outcome strings, with the matching metric
// increment.
//
// Returns a non-nil IncrementAppErrorResponse for every record;
// callers must Send() unconditionally.
func (a *appErrorsReceiver) handleOne(ctx context.Context, req *apidpb.IncrementAppErrorRequest) *apidpb.IncrementAppErrorResponse {
	out := &apidpb.IncrementAppErrorResponse{Fingerprint: req.GetFingerprint()}

	// ---- 1. Validate ----
	if err := validateIncrementRequest(req); err != nil {
		// Treat as redaction_failed (it's not — but the gateway
		// MUST NOT retry on InvalidArgument, and the metric
		// signal belongs on the same counter).
		a.observe(outcomeRedactionFailed)
		out.Outcome = outcomeRedactionFailed
		return out
	}

	// ---- 2. Resolve UUIDs ----
	accountID, err := uuid.Parse(req.GetAccountId())
	if err != nil {
		a.observe(outcomeRedactionFailed)
		out.Outcome = outcomeRedactionFailed
		return out
	}
	appID, err := uuid.Parse(req.GetAppId())
	if err != nil {
		a.observe(outcomeRedactionFailed)
		out.Outcome = outcomeRedactionFailed
		return out
	}
	var deploymentUUID state.UUID // NULL when empty
	if req.GetDeploymentId() != "" {
		deploymentID, err := uuid.Parse(req.GetDeploymentId())
		if err != nil {
			a.observe(outcomeRedactionFailed)
			out.Outcome = outcomeRedactionFailed
			return out
		}
		deploymentUUID = state.NewPgtypeUUID(deploymentID)
	}

	// ---- 3. Convert received_at ----
	receivedAtMS := req.GetReceivedAtUnixMs()
	receivedAt := state.NewPgtypeTime(msToTime(receivedAtMS))
	requestID := newRowID()
	if parsed, parseErr := uuid.Parse(req.GetRequestId()); parseErr == nil {
		requestID = parsed
	}

	// ---- 4. IncrementAppError (dedupe-merge INSERT) ----
	//
	// The SQL handles count + last_seen_at via ON CONFLICT DO
	// UPDATE; the params struct only carries the first_seen_at
	// fallback (used when the INSERT fires). See pkg/state/queries.sql
	// IncrementAppError for the full shape.
	//
	// inserted tells us whether the row was a fresh INSERT (true)
	// or an ON CONFLICT DO UPDATE (false) — Postgres exposes this
	// via the canonical (xmax = 0) trick on the RETURNING clause.
	// The wire-side outcome distinguishes "inserted" from
	// "merged" so the gateway can update its in-process LRU
	// freshness on the merge path.
	inserted, incErr := a.store.IncrementAppError(ctx, sqlc.IncrementAppErrorParams{
		ID:                      state.NewPgtypeUUID(newRowID()),
		AccountID:               state.NewPgtypeUUID(accountID),
		AppID:                   state.NewPgtypeUUID(appID),
		DeploymentID:            deploymentUUID,
		Fingerprint:             req.GetFingerprint(),
		Route:                   req.GetRouteTemplate(),
		HttpStatus:              int32(req.GetHttpStatus()),
		ErrorClass:              req.GetErrorClass(),
		SampleMessage:           req.GetSampleMessage(),
		FirstSeenAt:             receivedAt,
		LastInstanceID:          req.GetInstanceId(),
		LastNodeID:              req.GetNodeId(),
		LastRegion:              req.GetRegion(),
		LastCommitSha:           req.GetCommitSha(),
		LastDeploymentTag:       req.GetDeploymentTag(),
		LastDeploymentCreatedAt: req.GetDeploymentCreatedAt(),
		LastImageDigest:         req.GetImageDigest(),
	})
	if incErr != nil {
		if isConstraintViolation(incErr) {
			a.observe(outcomeDBError)
			out.Outcome = outcomeDBError
			return out
		}
		if isPgNotFound(incErr) {
			// FK violation: app_id or account_id is gone.
			// Treat as a benign no-op so the gateway doesn't
			// retry forever.
			a.observe(outcomeRedactionFailed)
			out.Outcome = outcomeRedactionFailed
			return out
		}
		a.observe(outcomeDBError)
		out.Outcome = outcomeDBError
		return out
	}

	// ---- 5. Insert the per-request row ----
	//
	// app_error_requests is per-request; the INSERT takes the
	// request_id (RequestID), a fresh row id (ID), and the
	// route template as `Route` (the SQL column name in
	// migrations/00222_app_errors.sql is route_template).
	reqErr := a.store.InsertAppErrorRequest(ctx, sqlc.InsertAppErrorRequestParams{
		ID:                  state.NewPgtypeUUID(newRowID()),
		AccountID:           state.NewPgtypeUUID(accountID),
		AppID:               state.NewPgtypeUUID(appID),
		Fingerprint:         req.GetFingerprint(),
		RequestID:           state.NewPgtypeUUID(requestID),
		ReceivedAt:          receivedAt,
		Route:               req.GetRouteTemplate(),
		HttpStatus:          int32(req.GetHttpStatus()),
		ErrorClass:          req.GetErrorClass(),
		SampleMessage:       "",
		DeploymentID:        deploymentUUID,
		HeadersSample:       []byte(req.GetHeadersSampleJson()),
		Redactions:          req.GetRedactionsApplied(),
		InstanceID:          req.GetInstanceId(),
		NodeID:              req.GetNodeId(),
		Region:              req.GetRegion(),
		CommitSha:           req.GetCommitSha(),
		DeploymentTag:       req.GetDeploymentTag(),
		DeploymentCreatedAt: req.GetDeploymentCreatedAt(),
		ImageDigest:         req.GetImageDigest(),
	})
	if reqErr != nil {
		// app_errors row was committed but the request row
		// failed. Count this as db_error; the next retention
		// purge will reconcile the count drift.
		a.observe(outcomeDBError)
		out.Outcome = outcomeDBError
		return out
	}

	a.observe("ok")
	if inserted {
		out.Outcome = outcomeInserted
		out.DedupeMerged = false
	} else {
		out.Outcome = outcomeMerged
		out.DedupeMerged = true
		// Bump the unlabelled dedupe-merge counter so the §12
		// "dedupe-effectiveness" panel sees the split between
		// fresh inserts and ON CONFLICT hits.
		if a.ops != nil {
			a.ops.ObserveAppErrorsDedupeMerge()
		}
	}
	return out
}

// observe is a nil-safe wrapper around ObserveAppErrorsRecorded.
func (a *appErrorsReceiver) observe(outcome string) {
	if a.ops == nil {
		return
	}
	a.ops.ObserveAppErrorsRecorded(outcome)
}

// validateIncrementRequest enforces the wire invariants. Returns
// nil on success; a non-nil status error otherwise.
func validateIncrementRequest(req *apidpb.IncrementAppErrorRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "nil request")
	}
	if req.GetAccountId() == "" {
		return status.Error(codes.InvalidArgument, "account_id required")
	}
	if req.GetAppId() == "" {
		return status.Error(codes.InvalidArgument, "app_id required")
	}
	if req.GetRouteTemplate() == "" {
		return status.Error(codes.InvalidArgument, "route_template required")
	}
	if req.GetFingerprint() == "" {
		return status.Error(codes.InvalidArgument, "fingerprint required")
	}
	if req.GetErrorClass() == "" {
		return status.Error(codes.InvalidArgument, "error_class required")
	}
	st := req.GetHttpStatus()
	if st < 400 || st > 599 {
		return status.Errorf(codes.InvalidArgument, "http_status %d out of range [400..599]", st)
	}
	if len(req.GetFingerprint()) != 64 {
		return status.Errorf(codes.InvalidArgument, "fingerprint must be 64 hex chars, got %d", len(req.GetFingerprint()))
	}
	// sample_message cap matches limits.AppErrorsSampleMessageCapBytes.
	if len(req.GetSampleMessage()) > api.AppErrorsSampleMessageCapBytes {
		return status.Errorf(codes.InvalidArgument, "sample_message too large: %d > %d",
			len(req.GetSampleMessage()), api.AppErrorsSampleMessageCapBytes)
	}
	// headers_sample is a JSON object; cap at 8 KiB.
	if len(req.GetHeadersSampleJson()) > 8*1024 {
		return status.Errorf(codes.InvalidArgument, "headers_sample too large: %d > 8192",
			len(req.GetHeadersSampleJson()))
	}
	if req.GetHeadersSampleJson() != "" {
		// Cheap parse — reject anything that isn't an object.
		var raw map[string]any
		if err := json.Unmarshal([]byte(req.GetHeadersSampleJson()), &raw); err != nil {
			return status.Errorf(codes.InvalidArgument, "headers_sample not valid JSON object: %v", err)
		}
		if len(raw) > 8 {
			return status.Errorf(codes.InvalidArgument, "headers_sample has %d keys; max 8", len(raw))
		}
	}
	return nil
}

// isConstraintViolation returns true if err is a Postgres CHECK /
// UNIQUE / FK violation (sqlstate 23xxx). Used to distinguish
// "real bad input" from "transient DB blip".
func isConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return len(pgErr.Code) >= 2 && pgErr.Code[:2] == "23"
	}
	return false
}

// isPgNotFound is the cmd/apid-side helper for "row not found".
// Mirrors pkg/state.isStateNotFound. Kept local so we don't add a
// cross-package dependency for a single error string.
func isPgNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "P0002" // no_data_found (raise)
	}
	if errors.Is(err, state.ErrNotFound) {
		return true
	}
	return false
}

// registerAppErrorsReceiver binds the AppErrorsServer onto a gRPC
// server. Called from runAppErrorsServer in main.go alongside the
// other gRPC services (Advisory etc).
func registerAppErrorsReceiver(s *grpc.Server, store appErrorsStore, ops *wire.OpsMetrics, enabled bool) {
	apidpb.RegisterAppErrorsServer(s, newAppErrorsReceiver(store, ops, enabled))
}

// newRowID returns a fresh UUID for the row's id column.
// app_errors.id and app_error_requests.id are uuid PKs; the
// column type is uuid (NOT NULL DEFAULT gen_random_uuid()) so
// callers may also rely on the DB-side default. We pre-populate
// here so the dedupe-merge path can route via the same ID
// shape (idempotent retries).
func newRowID() uuid.UUID { return uuid.New() }

// msToTime converts a unix-ms timestamp (the proto wire format)
// into a time.Time. Negative or zero returns the zero time so
// the caller's pgtype.Timestamptz surfaces as NULL.
func msToTime(ms int64) (t time.Time) {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
