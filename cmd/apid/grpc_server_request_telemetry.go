// apid-side gRPC handler for the RequestTelemetry service
// (ADR-127 PR-B). Wired by registerRequestTelemetryReceiver in
// main.go onto the same *grpc.Server runAppErrorsServer uses (or
// a sibling; the surface is independent).
//
// Direction: gatewayd-internal → apid. gatewayd-internal is the
// ONLY caller (CLAUDE.md ownership: apid is the sole writer to
// request_telemetry; gatewayd-internal never opens a direct
// Postgres connection for this store).
//
// Wire discipline mirrors grpc_server_apperrors.go: errors map to
// gRPC codes (InvalidArgument / ResourceExhausted / Internal),
// per-record outcomes ride on the response stream, transient
// failures do NOT abort the stream. PR-B adds a per-account
// token bucket so a sustained-overflow customer gets
// back-pressured without aborting the rest of the batch.

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Outcome strings for the IncrementRequestTelemetryResponse
// stream. The §12 metric
// `apid_request_telemetry_recorded_total{outcome="..."}` keys off
// these literals; the wire-side constants in pkg/wire/metrics.go
// must stay aligned (goconst-flagged so drift breaks the lint
// gate).
const (
	rtOutcomeInserted    = "inserted"
	rtOutcomeRateLimited = "rate_limited"
	rtOutcomeDBError     = "db_error"
)

// requestTelemetryStore is the Store subset the receiver needs.
// Declared as an interface here so unit tests can substitute a
// fake without spinning a real Postgres pool.
type requestTelemetryStore interface {
	AccountByID(ctx context.Context, id string) (state.Account, error)
	InsertRequestTelemetry(ctx context.Context, arg sqlc.InsertRequestTelemetryParams) error
}

// consumerUsageStore is the billing side of the receiver. PgStore and
// MemStore implement it. The receiver fails closed when it is absent so a
// rolling deployment cannot silently acknowledge requests without recording
// the financial usage fact.
type consumerUsageStore interface {
	RecordAPIConsumerUsage(context.Context, state.APIConsumerUsageEvent) (bool, error)
}

// telemetryRateLimiter is an alias for *peraccount.Limiter, kept as
// a named type so the rest of the file (and any external callers
// from cmd/apid/main.go) compile without churn. The implementation
// moved to pkg/ratelimit/peraccount in ADR-127 PR-D so the
// gatewayd-public OTel spans writer (PR-D Stage 3) can share the
// same per-account bucket semantics.
type telemetryRateLimiter = peraccount.Limiter

// requestTelemetryReceiver is the in-package server implementation
// of apidpb.RequestTelemetryServer. Wired by
// registerRequestTelemetryReceiver onto a *grpc.Server.
//
// ops is the apid daemon's *wire.OpsMetrics; nil-safe.
//
// limiter is the per-account token bucket pool; nil disables rate
// limiting (testing seam).
//
// enabled is the kill-switch (FAAS_REQUEST_TELEMETRY_ENABLED).
// When false, IncrementRequestTelemetry returns codes.Unavailable
// so the gateway stops sending.
type requestTelemetryReceiver struct {
	apidpb.UnimplementedRequestTelemetryServer
	store   requestTelemetryStore
	ops     *wire.OpsMetrics
	limiter *telemetryRateLimiter
	enabled bool
}

// newRequestTelemetryReceiver wires a production receiver.
func newRequestTelemetryReceiver(store requestTelemetryStore, ops *wire.OpsMetrics, limiter *telemetryRateLimiter, enabled bool) *requestTelemetryReceiver {
	if limiter == nil {
		limiter = peraccount.NewLimiter()
	}
	return &requestTelemetryReceiver{store: store, ops: ops, limiter: limiter, enabled: enabled}
}

// IncrementRequestTelemetry streams per-record telemetry rows
// from the gateway edge. The server commits each record inside
// its own transaction (per-record commit; load-bearing for the
// rate-limit-then-continues shape — a rate-limited row MUST NOT
// abort the batch).
//
// Outcome ∈ {inserted, rate_limited, db_error}. The first is a
// success; the latter two are observability signals — the gateway
// MUST NOT retry on them (a retry would double-count).
func (r *requestTelemetryReceiver) IncrementRequestTelemetry(stream apidpb.RequestTelemetry_IncrementRequestTelemetryServer) error {
	if !r.enabled {
		return status.Error(codes.Unavailable, "request_telemetry disabled by FAAS_REQUEST_TELEMETRY_ENABLED")
	}
	for {
		req, err := stream.Recv()
		if err != nil {
			// io.EOF or context cancel: end of stream.
			//nolint:nilerr // io.EOF on stream.Recv is the canonical "client half-closed" signal — returning nil closes the stream cleanly without surfacing the EOF as an error.
			return nil
		}
		out := r.handleOne(stream.Context(), req)
		if err := stream.Send(out); err != nil {
			return err
		}
	}
}

// handleOne processes a single record and returns the per-record
// outcome. Never returns an error — every failure path maps to
// one of the closed outcome strings, with the matching metric
// increment.
func (r *requestTelemetryReceiver) handleOne(ctx context.Context, req *apidpb.IncrementRequestTelemetryRequest) *apidpb.IncrementRequestTelemetryResponse {
	out := &apidpb.IncrementRequestTelemetryResponse{}

	// ---- 1. Validate UUIDs ----
	accountID, err := uuid.Parse(req.GetAccountId())
	if err != nil {
		r.observe(rtOutcomeDBError)
		out.Outcome = rtOutcomeDBError
		return out
	}
	appID, err := uuid.Parse(req.GetAppId())
	if err != nil {
		r.observe(rtOutcomeDBError)
		out.Outcome = rtOutcomeDBError
		return out
	}
	deploymentID, err := uuid.Parse(req.GetDeploymentId())
	if err != nil {
		r.observe(rtOutcomeDBError)
		out.Outcome = rtOutcomeDBError
		return out
	}
	var consumerID pgtype.UUID
	consumerKey := state.AnonymousConsumerKey
	if raw := req.GetConsumerId(); raw != "" {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			r.observe(rtOutcomeDBError)
			out.Outcome = rtOutcomeDBError
			return out
		}
		consumerID = state.NewPgtypeUUID(parsed)
		consumerKey = parsed.String()
	}

	count := int(req.GetCount())
	if count < 1 {
		count = 1
	}
	if req.GetHttpStatus() < 100 || req.GetHttpStatus() > 599 {
		r.observe(rtOutcomeDBError)
		out.Outcome = rtOutcomeDBError
		return out
	}
	windowStart := msToTime(req.GetReceivedAtUnixMs())
	if windowStart.IsZero() {
		windowStart = time.Now().UTC()
	}
	windowStart = windowStart.UTC().Truncate(time.Minute)
	eventID := req.GetEventId()
	if eventID == "" {
		// Compatibility for an older gateway during a rolling upgrade. The
		// fallback is deterministic for the same collapsed payload, so a
		// response-loss retry remains idempotent even before all gateways
		// carry event_id.
		eventID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s/%s/%s/%d/%d/%d/%s/%s/%d/%s/%s/%s", accountID, appID, consumerKey, windowStart.Unix(), req.GetHttpStatus(), count, req.GetRouteTemplate(), req.GetMethod(), req.GetLatencyMs(), req.GetTraceId(), req.GetWakeId(), req.GetInstanceId()))).String()
	}
	var errorCount int64
	if req.GetHttpStatus() >= 400 {
		errorCount = int64(count)
	}
	usageStore, ok := r.store.(consumerUsageStore)
	if !ok {
		r.observe(rtOutcomeDBError)
		out.Outcome = rtOutcomeDBError
		return out
	}
	_, usageErr := usageStore.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{
		EventID: eventID, AccountID: accountID.String(), AppID: appID.String(),
		ConsumerKey: consumerKey, WindowStart: windowStart,
		RequestCount: int64(count), ErrorCount: errorCount, BillableUnits: int64(count),
	})
	if usageErr != nil {
		r.observe(rtOutcomeDBError)
		out.Outcome = rtOutcomeDBError
		return out
	}

	// ---- 2. Resolve per-account rate cap ----
	limits, ok := r.limiter.CachedLimits(accountID)
	if !ok {
		acct, accErr := r.store.AccountByID(ctx, accountID.String())
		if accErr != nil {
			// Account gone — treat as db_error so the gateway
			// doesn't retry forever.
			r.observe(rtOutcomeDBError)
			out.Outcome = rtOutcomeDBError
			return out
		}
		limits = api.MustLimitsFor(acct.Plan)
		r.limiter.CacheLimits(accountID, limits)
	}
	if !limits.DebugTelemetryEnabled {
		// Plan doesn't include telemetry — same as rate-limit
		// overflow: drop the row, count as rate_limited (the
		// gateway should stop sending, same code path).
		out.Outcome = rtOutcomeRateLimited
		out.RetryAfterMs = 60_000
		return out
	}

	// ---- 3. Per-account token bucket check ----
	taken, retryAfter := r.limiter.Take(accountID, limits.DebugTelemetryRequestsPerMinute)
	if !taken {
		out.Outcome = rtOutcomeRateLimited
		out.RetryAfterMs = retryAfter
		// Counter carries `rate_limited` as a label so dashboards
		// can split insert success vs bucket-overflow cleanly.
		r.observe(rtOutcomeRateLimited)
		return out
	}

	// ---- 4. INSERT ----
	// Wire compatibility: older gateways do not send the additive dimension
	// fields. Map proto3 empty defaults to the database sentinels so a rolling
	// upgrade continues to insert rows while new gateways populate dimensions.
	uaFamily := req.GetUaFamily()
	if uaFamily == "" {
		uaFamily = "__unknown__"
	}
	referrerHost := req.GetReferrerHost()
	if referrerHost == "" {
		referrerHost = "__none__"
	}
	country := req.GetCountry()
	if country == "" {
		country = "__unknown__"
	}
	guestRuntime := req.GetGuestRuntime()
	if guestRuntime == "" {
		guestRuntime = "__unknown__"
	}
	guestOutcome := req.GetGuestOutcome()
	if guestOutcome == "" {
		guestOutcome = "missing"
	}
	guestErrorClass := req.GetGuestErrorClass()
	if req.GetGuestDurationMs() < 0 || req.GetGuestDurationMs() > 86400000 ||
		!validRequestTelemetryGuestRuntime(guestRuntime) ||
		!validRequestTelemetryGuestOutcome(guestOutcome) ||
		!validRequestTelemetryGuestErrorClass(guestErrorClass) {
		r.observe(rtOutcomeDBError)
		out.Outcome = rtOutcomeDBError
		return out
	}
	insertErr := r.store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
		AccountID:       state.NewPgtypeUUID(accountID),
		AppID:           state.NewPgtypeUUID(appID),
		DeploymentID:    state.NewPgtypeUUID(deploymentID),
		Route:           req.GetRouteTemplate(),
		Method:          req.GetMethod(),
		Status:          int32(req.GetHttpStatus()),
		LatencyMs:       int32(req.GetLatencyMs()),
		ColdBoot:        req.GetColdBoot(),
		TraceID:         pgtype.Text{String: req.GetTraceId(), Valid: req.GetTraceId() != ""},
		ReceivedAt:      state.NewPgtypeTime(msToTime(req.GetReceivedAtUnixMs())),
		Count:           int32(count),
		UaFamily:        uaFamily,
		ReferrerHost:    referrerHost,
		Country:         country,
		WakeID:          pgtype.Text{String: req.GetWakeId(), Valid: req.GetWakeId() != ""},
		InstanceID:      pgtype.Text{String: req.GetInstanceId(), Valid: req.GetInstanceId() != ""},
		GuestDurationMs: int32(req.GetGuestDurationMs()),
		GuestRuntime:    guestRuntime,
		GuestOutcome:    guestOutcome,
		GuestErrorClass: guestErrorClass,
		ConsumerID:      consumerID,
	})
	if insertErr != nil {
		if isConstraintViolation(insertErr) {
			r.observe(rtOutcomeDBError)
			out.Outcome = rtOutcomeDBError
			return out
		}
		var pgErr *pgconn.PgError
		// Check if it's a CHECK constraint violation
		// (count >= 1, status BETWEEN 100..599, etc).
		if errorsAsPgError(insertErr, &pgErr) && pgErr.Code == "23514" {
			r.observe(rtOutcomeDBError)
			out.Outcome = rtOutcomeDBError
			return out
		}
		r.observe(rtOutcomeDBError)
		out.Outcome = rtOutcomeDBError
		return out
	}

	r.observe(rtOutcomeInserted)
	out.Outcome = rtOutcomeInserted
	return out
}

func validRequestTelemetryGuestRuntime(value string) bool {
	switch value {
	case "node22", "node24", "python312", "python313", "go124", "__unknown__":
		return true
	default:
		return false
	}
}

func validRequestTelemetryGuestOutcome(value string) bool {
	switch value {
	case "ok", "http_error", "handler_error", "timeout", "canceled", "missing":
		return true
	default:
		return false
	}
}

func validRequestTelemetryGuestErrorClass(value string) bool {
	switch value {
	case "", "http_5xx", "handler_exec", "handler_protocol", "timeout", "canceled":
		return true
	default:
		return false
	}
}

// observe is a nil-safe wrapper around the recorded counter.
func (r *requestTelemetryReceiver) observe(outcome string) {
	if r.ops == nil {
		return
	}
	r.ops.IncrementRequestTelemetryRecorded(outcome)
}

// registerRequestTelemetryReceiver binds the RequestTelemetryServer
// onto a gRPC server. Called from runRequestTelemetryServer in
// main.go alongside the other gRPC services.
func registerRequestTelemetryReceiver(s *grpc.Server, store requestTelemetryStore, ops *wire.OpsMetrics, limiter *telemetryRateLimiter, enabled bool) {
	apidpb.RegisterRequestTelemetryServer(s, newRequestTelemetryReceiver(store, ops, limiter, enabled))
}

// errorsAsPgError is a tiny helper that returns true when err is
// (or wraps) a *pgconn.PgError, populating *target. Delegates to
// errors.As so the errorlint pass is happy and the unwrap chain is
// semantically correct for pgx v5.
func errorsAsPgError(err error, target **pgconn.PgError) bool {
	return errors.As(err, target)
}
