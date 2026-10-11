// Advisory gRPC receiver — Wave 0 PR-C / ADR-047. Binds
// /run/faas/apid.sock and dispatches vmmd-forwarded guest-init
// fanotify batches to pkg/audit.Auditor.Emit("stateless.advisory", ...).
//
// Direction: vmmd → apid only. apid never dials this service; the
// depguard apid-control-plane-only deny list (.golangci.yml) blocks
// the reverse direction. See api/proto/onebox/faas/apid/v1/advisory.proto
// for the wire shape and ADR-047 for the rationale.

package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// advisoryStore is the minimal slice of state.Store the receiver needs.
// Defined as an interface so the receiver can be unit-tested without
// spinning up a real pgxpool (state.Store is already the canonical
// seam — see pkg/state/store.go::AppByID).
type advisoryStore interface {
	AppByID(ctx context.Context, id string) (state.App, error)
}

// auditEmitter is the minimal *auditor surface the receiver needs.
// Defined as an interface so the receiver can be unit-tested without
// standing up a real pgxpool. *auditor (cmd/apid/audit.go:148) already
// satisfies this signature; the production wiring is unchanged.
type auditEmitter interface {
	Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
}

// advisoryReceiver is the in-package server implementation of
// apidpb.AdvisoryServer. Wired by registerAdvisoryReceiver onto a
// *grpc.Server that runAdvisoryServer in main.go owns.
//
// ops is the apid daemon's *wire.OpsMetrics — used to increment
// the stateless_advisory_events_total counter on each landed
// advisory (Mega-PR B). Pass nil if metrics are not wired; the
// accessor is nil-receiver safe and unit tests that don't wire
// metrics keep working without stubs.
type advisoryReceiver struct {
	apidpb.UnimplementedAdvisoryServer
	store  advisoryStore
	audit  auditEmitter
	notif  Notifier
	logger slogLike
	ops    *wire.OpsMetrics
}

// slogLike is the minimal *slog.Logger surface the receiver needs.
// Tests inject a no-op stub; production wires the daemon's *slog.Logger.
type slogLike interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
	Debug(msg string, args ...any)
}

// ForwardStatelessAdvisory writes one audit row per call. ADR-035:
// best-effort, never rolls back; on audit write failure we DROP the
// row and log Warn — this is observation, not source of truth.
//
// Mapping errors back to gRPC codes mirrors pkg/vmmdgrpc/server.go:
//   - missing/invalid args   → codes.InvalidArgument
//   - app row not found       → codes.NotFound (lets vmmd's retry
//     logic distinguish "app genuinely gone" from "transient DB blip")
//   - everything else         → codes.Internal
func (a *advisoryReceiver) ForwardStatelessAdvisory(ctx context.Context, req *apidpb.ForwardStatelessAdvisoryRequest) (*apidpb.ForwardStatelessAdvisoryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "nil request")
	}
	if req.AppId == "" {
		return nil, status.Error(codes.InvalidArgument, "app_id required")
	}
	if req.Instance == "" {
		return nil, status.Error(codes.InvalidArgument, "instance required")
	}

	// Resolve account_id from app_id so the audit row is subject-pinned
	// (cmd/apid/handlers_audit.go filters by acct.ID). If the app row
	// is gone between wake and advisory we surface the advisory with
	// subject=NULL; the include_anonymous query param on /v1/audit-events
	// surfaces those rows.
	var subject *string
	if a.store != nil {
		app, err := a.store.AppByID(ctx, req.AppId)
		switch {
		case err == nil && app.AccountID != "":
			acct := app.AccountID
			subject = &acct
		case isStateNotFound(err):
			return nil, status.Error(codes.NotFound, fmt.Sprintf("app %s not found", req.AppId))
		case err != nil:
			a.logger.Warn("advisory: app lookup failed; emitting without subject", "app_id", req.AppId, "err", err)
		}
	}

	// Translate the proto events into a map shape pkg/audit can
	// serialise. count + instance are denormalised for ops dashboards
	// (so a single /v1/audit-events query answers "which instance
	// wrote the most"). severity is the highest-severity path in
	// the batch (Move 1 PR-A) so a single /v1/audit-events row can
	// be triaged without inspecting the events[] list. Computed at
	// emit time so the audit row is self-describing — a reader
	// doesn't need the closed-path list to know whether the row is
	// "high" (/data, /db, /var/lib/postgresql, /var/lib/mysql) or
	// "warn" (other closed paths).
	//
	// Mega-PR B: compute the severity ONCE per call. The audit row's
	// `data.severity` field and the counter increment use the same
	// value so they can't drift if severityForPath ever gains a new
	// branch (e.g. a future "low" tier). advisoryBatchSeverity's
	// vocabulary is the receiver-internal set (severityHigh /
	// severityWarn / severityInfo) and is exactly the wire-side
	// counter vocabulary (AdvisorySeverity{High,Warn,Info}) so no
	// translation is needed.
	severity := advisoryBatchSeverity(req.Events)
	data := map[string]any{
		"instance": req.Instance,
		"app_id":   req.AppId,
		"count":    len(req.Events),
		"events":   advisoryEventsToMap(req.Events),
		"severity": severity,
	}

	if a.audit != nil {
		// TODO: when auditEmitter.Emit grows an error return, the
		// counter below must move inside the success branch — the
		// pair-counter invariant (apid events vs vmmd ok) requires
		// the audit row to be durably stored before the metric
		// increments. Today Emit swallows write-failures into its
		// own internal counter, so the order is currently safe.
		a.audit.Emit(ctx, "stateless.advisory", subject, data)
	}

	// Mega-PR B: increment apid_stateless_advisory_events_total
	// off the batch severity we just computed. Pair-counter with
	// vmmd_stateless_advisory_batches_emitted_total{result="ok"} —
	// a healthy box has rate(apid_..)[5m] ≈ rate(vmmd_..{ok})[5m].
	// Placed AFTER the audit emit so a hypothetical write-failure
	// path (audit row missing) doesn't double-count on the metric;
	// the audit seam is best-effort and the metric is observation-
	// only, but the order keeps the two surfaces consistent.
	a.ops.ObserveStatelessAdvisory(severity)

	// Fire the pg_notify channel that cmd/apid/handlers_events.go
	// subscribes to for live SSE frames. The payload is a SMALL
	// summary (n + sample_path) — the detail surface is the audit
	// row at /v1/audit-events?kind_prefix=stateless.advisory, which
	// the SSE client fetches via the URL handed back in the frame.
	if a.notif != nil {
		sample := ""
		if len(req.Events) > 0 && req.Events[0] != nil {
			sample = req.Events[0].Path
		}
		np, _ := json.Marshal(map[string]any{
			"app_id":      req.AppId,
			"instance":    req.Instance,
			"n":           len(req.Events),
			"sample_path": sample,
		})
		// Best-effort — a dropped notify means the dashboard SSE
		// misses this frame, but the audit row at /v1/audit-events
		// is still there. ADR-035.
		_ = a.notif.Notify(ctx, db.NotifyStatelessAdvisory, string(np))
	}

	return &apidpb.ForwardStatelessAdvisoryResponse{}, nil
}

// advisoryEventsToMap converts the proto events into the map shape
// pkg/audit expects. The mask []string is collapsed to a comma-joined
// string for cheap grep in the audit log JSON column.
func advisoryEventsToMap(in []*apidpb.AdvisoryEvent) []map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(in))
	for _, e := range in {
		if e == nil {
			continue
		}
		out = append(out, map[string]any{
			"path":       e.Path,
			"mask":       joinMasksForJSON(e.Mask),
			"pid":        e.Pid,
			"ts_unix_ms": e.TsUnixMs,
		})
	}
	return out
}

func joinMasksForJSON(masks []string) string {
	if len(masks) == 0 {
		return ""
	}
	out := ""
	for i, m := range masks {
		if i > 0 {
			out += ","
		}
		out += m
	}
	return out
}

// Severity labels written into the stateless.advisory audit row's
// "severity" field (Move 1 PR-A). Defined here as named consts so
// the goconst gate doesn't fire (test and receiver share the same
// vocabulary, and the previous string literals appeared >3× each).
// Mirrors pkg/dashboard/templates/audit_events.html's .badge.high /
// .badge.warn / .badge.info CSS — a future vocabulary change means
// touching both.
const (
	severityHigh = "high"
	severityWarn = "warn"
	severityInfo = "info"
)

// statelessAdvisoryHighPaths is the subset of the closed-path list
// that warrants the "high" severity badge (Move 1 PR-A). Mirrors
// pkg/dashboard/dashboard.go's StatelessClosedPaths severity field
// and guest/init/stateless_advisory_linux.go's statelessRuntimePaths.
// Kept here as the single source of truth for the apid-side emit;
// the dashboard mirrors the same set so a future path addition
// means touching both — see ADR-048 for the lockstep rationale.
var statelessAdvisoryHighPaths = []string{
	"/data",
	"/db",
	"/var/lib/postgresql",
	"/var/lib/mysql",
}

// severityForPath returns severityHigh / severityWarn for a
// closed-path string, or severityInfo for paths outside the closed
// list. Pure function — the events list is processed by
// advisoryBatchSeverity. Tests pin the classification
// (advisory_receiver_test.go).
//
// Match rule: a path is "high" if it equals one of the high-paths
// OR is a strict sub-path (e.g. /data/foo matches /data, /db/log
// matches /db). The closed-path list is the *root* of each
// watched directory; the guest-init fanotify watcher matches by
// prefix (spec §17 G13), so we mirror that here. Without the
// prefix match, a customer writing /data/foo would render as
// "warn" because /data/foo isn't a member of the high list.
func severityForPath(p string) string {
	for _, hp := range statelessAdvisoryHighPaths {
		if p == hp || strings.HasPrefix(p, hp+"/") {
			return severityHigh
		}
	}
	return severityWarn
}

// advisoryBatchSeverity returns the highest severity in the batch.
// A batch with any "high" path is severityHigh; otherwise
// severityWarn. An empty batch is severityInfo (no paths to
// classify — defensive default so the audit row's severity field
// is always populated).
func advisoryBatchSeverity(events []*apidpb.AdvisoryEvent) string {
	if len(events) == 0 {
		return severityInfo
	}
	high := false
	for _, e := range events {
		if severityForPath(e.Path) == severityHigh {
			high = true
			break
		}
	}
	if high {
		return severityHigh
	}
	return severityWarn
}

// isStateNotFound recognises pkg/state's "not found" sentinel.
// Wave 1 follow-up: lift a typed sentinel from pkg/state if the
// convention shifts; for Wave 0 we match on the error string to keep
// the receiver decoupled from the pgstore internals.
func isStateNotFound(err error) bool {
	if err == nil {
		return false
	}
	// state.ErrNotFound sentinel — pkg/state/store.go exports it.
	if errors.Is(err, state.ErrNotFound) {
		return true
	}
	// Defensive: pgx returns pgx.ErrNoRows, which state.PgStore
	// wraps as state.ErrNotFound in production. If a stub returns
	// the bare pgx error, fall through to substring check.
	s := err.Error()
	return strContains(s, "not found") || strContains(s, "no rows")
}

// strContains is a no-import helper. Kept local to avoid colliding
// with cmd/apid/main_test.go's contains helper (different signature).
func strContains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// registerAdvisoryReceiver binds the AdvisoryServer onto a gRPC
// server. Called from runAdvisoryServer in main.go alongside the
// HTTP server lifecycle.
//
// ops is the apid daemon's *wire.OpsMetrics — passed through to
// the receiver so it can increment
// apid_stateless_advisory_events_total on each landed advisory
// (Mega-PR B). Pass nil if metrics are not wired; the accessor is
// nil-receiver safe.
func registerAdvisoryReceiver(s *grpc.Server, store advisoryStore, audit *auditor, notif Notifier, logger slogLike, ops *wire.OpsMetrics) {
	apidpb.RegisterAdvisoryServer(s, &advisoryReceiver{
		store:  store,
		audit:  audit,
		notif:  notif,
		logger: logger,
		ops:    ops,
	})
}
