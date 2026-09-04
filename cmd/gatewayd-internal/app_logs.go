// cmd/gatewayd-internal AppLogsHandler — issue #254 / Move 4 PR-2 production
// wiring. The customer-facing `GET /v1/apps/{slug}/logs` route
// moves from cmd/apid (PR-A) to cmd/gatewayd-internal (PR-2) because
// gatewayd-internal sits directly behind gatewayd-public (spec §11)
// and already imports pkg/scheddgrpc (ADR-018, ADR-043). The auth chain
// (bearer / session / MFA / scope / IDOR-safe LoadApp) is shared
// with cmd/apid via pkg/auth.Middleware (ADR-046).
//
// Why this lives in gatewayd-internal and not apid: depguard
// (`.golangci.yml` apid-control-plane-only) forbids cmd/apid from
// importing pkg/scheddgrpc. Replicating the scheddClient bridge
// apid shipped in PR-A would leak the gRPC transport into the
// control plane; the clean owner is the daemon that already
// dials schedd. ADR-043 records the architectural commitment.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// logStreamer is the minimal surface the AppLogsHandler needs
// from the schedd client. *scheddgrpc.Client satisfies it; the
// whitebox tests inject a controllable stub that satisfies the
// interface without standing up a real gRPC dial — that's the
// point of the interface. Reusing *scheddgrpc.Client directly
// would force every test through `//go:build metal` and a fake
// gRPC server, neither of which fits the unit-test loop. See
// cmd/gatewayd-internal/app_logs_test.go::controllableScheddClient.
//
// sinceWrittenAt (issue #517 / PR-B, AC3) is the RFC3339 lower
// bound the handler forwards to schedd for the per-instance
// ring filter; zero = no time bound. deploymentID (AC3) scopes
// the per-instance goroutine fan-out to one deployment; empty =
// all live instances.
//
// level + grep (issue #309 / tier-2 DX) are the customer-facing
// --level / --grep filter values; both empty = no filter. The
// gateway already validated the level enum + grep regex at
// parse time (apislogs.ValidateLogFilters), so this method
// receives pre-validated strings and forwards them verbatim.
type logStreamer interface {
	StreamAppLogs(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, level string, grep string) (scheddgrpc.LogStream, error)
}

// logStreamerResolver is the per-app dial factory the
// AppLogsHandler uses to reach the owner schedd (Phase 2 / Gate
// A). Production wires it to a closure that calls
// scheddRouter.ScheddForApp; tests pass a stub that returns a
// fixed logStreamer for every app. The two-field shape keeps the
// handler decoupled from state.App — it never sees the
// apps.node_id, only the resolved client.
type logStreamerResolver interface {
	ScheddForApp(ctx context.Context, appID string) (logStreamer, error)
}

// AppLogsHandler owns `GET /v1/apps/{slug}/logs`. The route is
// mounted on the gatewayd-internal mux before the apidProxy wrapper
// (cmd/gatewayd-internal/main.go) so the apid loopback proxy never sees
// the path — the carve-out lives in isApidLogsPath
// (cmd/gatewayd-internal/proxy.go) per the §11 single-public-listener
// invariant.
//
// The handler composes four services:
//
//   - *mwauth.Middleware — the pkg/auth.Middleware apid shares
//     with gatewayd-internal. Carries the bearer / session / MFA / scope
//     gate + the IDOR-safe LoadApp. Same AuthLimit bucket the
//     apid routes use, so the spec §11 "10/min/IP" rule covers
//     both gateways.
//   - logStreamerResolver — Phase 2 / Gate A: a per-app dial
//     factory that returns the schedd client that owns the
//     given app. Production wires it to scheddRouter.ScheddForApp;
//     tests inject a stub that returns a fixed logStreamer for
//     every app. The two-field shape keeps the handler decoupled
//     from state.App — it never sees apps.node_id, only the
//     resolved client.
//   - state.Store — backs both the IDOR-safe LoadApp call
//     (slugs → apps) and the parked-app pre-flight
//     (ListInstancesForApp). The same *state.PgStore pointer
//     satisfies both.
//   - *wire.OpsMetrics — per-frame `apid_logs_emitted_total`
//     counter; nil-safe so unit tests don't need a registry.
type AppLogsHandler struct {
	Auth      *mwauth.Middleware
	ScheddFor logStreamerResolver
	Store     state.Store
	Log       *slog.Logger
	Ops       *wire.OpsMetrics

	// Heartbeat is the SSE liveness interval. Defaults to 15s in
	// production (cmd/gatewayd-internal/main.go); tests shorten it to a
	// few ms so timer cases don't have to wait the production
	// interval.
	Heartbeat time.Duration
	// Backstop is the SSE stream idle timeout. Defaults to 10
	// minutes in production; tests shorten it to bound the wall
	// time of the test cases.
	Backstop time.Duration
}

// defaultAppLogsHeartbeat is the production SSE heartbeat. The
// htmx-ext-sse library treats silence as a dead connection; 15s
// keeps the connection alive without flooding the wire.
const defaultAppLogsHeartbeat = 15 * time.Second

// defaultAppLogsBackstop is the production SSE backstop. After
// 10 minutes of idle, the handler emits `event: end {"reason":
// "timeout"}` and cancels the schedd RPC. The 10-minute value
// matches cmd/apid's pre-PR-2 default.
const defaultAppLogsBackstop = 10 * time.Minute

// ServeHTTP is the http.Handler entry point so the AppLogsHandler
// can be mounted directly on a *http.ServeMux. The route is
// `GET /v1/apps/{slug}/logs` so r.PathValue("slug") always has a
// value. The auth chain is composed with the pkg/auth.Middleware
// surface area:
//
//	RequireLimited (auth + AuthLimit) -> RequireMFA -> RequireScope -> inner
//
// RequireScope uses api.ScopesReadSurface — the same set apid's
// pre-PR-2 route used. RequireLimited shares the per-IP bucket
// with the apid routes so a customer cannot bypass the §11
// rate-limit by switching gateways.
func (h *AppLogsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	inner := mwauth.AccountHandler(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		h.stream(w, r, acct, slug)
	})
	chain := h.Auth.RequireScope(api.ScopesReadSurface...)(h.Auth.RequireMFA(inner))
	h.Auth.RequireLimited(chain)(w, r)
}

// stream is the inner handler: IDOR-safe LoadApp, filter
// validation, then the schedd dial + receive pump. The flow
// mirrors the legacy cmd/apid/handlers_ext.go::streamAppLogs
// 1:1 so the wire shape (and the 7 whitebox tests moved over
// from cmd/apid/schedd_client_test.go) keep their contracts.
func (h *AppLogsHandler) stream(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) {
	app, ok := h.Auth.LoadApp(w, r, acct, slug)
	if !ok {
		// LoadApp already wrote a 404 problem. Nothing to do.
		return
	}
	if _, err := h.Store.ListInstancesForApp(r.Context(), app.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"No running instance", "the app is parked; wake it first"))
		return
	}
	level, grep, sinceWrittenAt, deploymentID, reason, ok := apislogs.ValidateLogFilters(r)
	if !ok {
		apislogs.StartSSE(w)
		flusher, _ := w.(http.Flusher)
		switch reason {
		case apislogs.InvalidLevelCode:
			apislogs.WriteInvalidLevelError(w, flusher)
		case apislogs.InvalidGrepCode:
			apislogs.WriteInvalidGrepError(w, flusher)
		case apislogs.InvalidSinceCode:
			apislogs.WriteInvalidSinceError(w, flusher)
		}
		return
	}
	// Plan-gate the ?deployment= filter (issue #517 / PR-B, AC3).
	// Free customers (LogDeploymentFilterMax == 0) get a hard
	// rejection; Hobby+ may scope to N deployments per the per-plan
	// table in pkg/api/limits.go. The wire surface today is
	// single-valued so we compare directly against the cap. The
	// rejection is a one-liner that delegates to enforceDeploymentFilter
	// so the plan-gate rule has its own whitebox coverage without
	// needing to drive the full Auth + Store chain.
	if deploymentID != "" {
		if !enforceDeploymentFilter(w, acct.Plan) {
			return
		}
	}
	sinceSeq := apislogs.ParseInt64Query(r, "since_seq", 0)

	apislogs.StartSSE(w)
	flusher, _ := w.(http.Flusher)
	h.serveAppLogsWithIdentity(r.Context(), w, flusher, app.ID, acct.ID, app.ID, sinceSeq, sinceWrittenAt, deploymentID, level, grep)
}

// serveAppLogs is the receive-pump body. Mirrors the legacy
// cmd/apid/handlers_ext.go::serveAppLogs 1:1 — the receive
// goroutine pattern, the timer ticker, the channel-of-1 drop
// policy, and the io.EOF / codes.NotFound delegation all carry
// over. The split between `stream` and `serveAppLogs` is the
// load-test seam — the 9 whitebox tests in this package drive
// serveAppLogs directly with a stub stream without standing up
// the auth + LoadApp chain.
//
// level + grep (issue #309 / tier-2 DX) are forwarded to
// schedd as-is; both empty = no filter at the schedd sink.
func (h *AppLogsHandler) serveAppLogs(ctx_ context.Context, w http.ResponseWriter, flusher http.Flusher, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, level string, grep string) {
	h.serveAppLogsWithIdentity(ctx_, w, flusher, appID, "", appID, sinceSeq, sinceWrittenAt, deploymentID, level, grep)
}

func (h *AppLogsHandler) serveAppLogsWithIdentity(ctx_ context.Context, w http.ResponseWriter, flusher http.Flusher, appID, accountID, appIdentity string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, level string, grep string) {
	// Phase 2 / Gate A: resolve the owner schedd for appID via
	// the per-node router. The fallback path (legacy single
	// schedd) is gone — the router covers the single-box
	// default-local fleet identically to the multi-box case.
	sched, err := h.ScheddFor.ScheddForApp(ctx_, appID)
	if err != nil {
		apislogs.RenderAppLogsError(w, flusher, err)
		return
	}
	stream, err := sched.StreamAppLogs(ctx_, appID, sinceSeq, sinceWrittenAt, deploymentID, level, grep)
	if err != nil {
		// codes.Unimplemented from the stub → "schedd not wired
		// (dev mode)"; codes.NotFound from a real schedd → "no
		// live instances". Both render as the SSE degraded
		// envelope so the SDK surfaces a stable Error code.
		apislogs.RenderAppLogsError(w, flusher, err)
		return
	}
	heartbeat := h.Heartbeat
	if heartbeat <= 0 {
		heartbeat = defaultAppLogsHeartbeat
	}
	backstop := h.Backstop
	if backstop <= 0 {
		backstop = defaultAppLogsBackstop
	}
	// Derive a cancellable context so the receive goroutine
	// exits the moment the handler goroutine returns (ctx
	// cancel, terminal frame, or backstop).
	streamCtx, cancelStream := context.WithCancel(ctx_)
	defer cancelStream()

	// Capacity 1 + non-blocking send keeps the receive goroutine
	// from blocking when the SSE writer is slow; a dropped frame
	// is no worse than a missed frame on a quiet stream because
	// the heartbeat keeps the client alive.
	//
	// Terminal-result carve-out (gatewayd flake surfaced 2026-08-24,
	// dispatch 32718562570 / TestServeAppLogs_GenericErrorDelegatesToRenderAppLogsError):
	// a non-EOF error from Recv must NEVER be dropped on the
	// non-blocking `default:` path, because the handler's only
	// signal that an error occurred is the closed recvCh — if the
	// terminal recvResult is dropped, the handler's `!ok` branch
	// fires and emits `event: end\ndata: {}` (clean-EOF frame)
	// instead of the RenderAppLogsError envelope the SDK is
	// pinned to. ~6% failure rate under -race -count=50 because
	// the handler can race behind the receive goroutine exactly
	// once on the second Recv (1st result still buffered).
	// Resolve by splitting the send into two paths: regular frames
	// may drop; terminal results block until the handler consumes
	// them or the streamCtx cancels.
	recvCh := make(chan recvResult, 1)
	go func() {
		defer close(recvCh)
		for {
			f, err := stream.Recv()
			select {
			case <-streamCtx.Done():
				return
			default:
			}
			if err == nil {
				// Regular frame — drop rather than block
				// when the SSE writer is behind.
				select {
				case recvCh <- recvResult{frame: f, err: nil}:
				case <-streamCtx.Done():
					return
				default:
					// Channel full — drop. The heartbeat
					// keeps the client alive, and the
					// per-instance ring is the durable
					// source of truth.
				}
				continue
			}
			// Terminal result (any non-nil err from Recv,
			// including io.EOF / codes.NotFound / generic).
			// Block until the handler consumes it OR the
			// streamCtx cancels; do not drop. If we drop a
			// terminal error here the handler emits the
			// clean-EOF `event: end\ndata: {}` frame instead
			// of the RenderAppLogsError envelope the SDK
			// decodes.
			select {
			case recvCh <- recvResult{frame: f, err: err}:
			case <-streamCtx.Done():
				return
			}
			return
		}
	}()

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	backstopTimer := time.NewTimer(backstop)
	defer backstopTimer.Stop()
	for {
		select {
		case <-ctx_.Done():
			return
		case <-backstopTimer.C:
			_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"timeout\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ":\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case r, ok := <-recvCh:
			if !ok {
				// Receive goroutine exited without writing
				// a terminal result — treat as a clean EOF
				// so structured-frame consumers exit the
				// loop.
				_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
				if flusher != nil {
					flusher.Flush()
				}
				return
			}
			if r.err != nil {
				// io.EOF = clean shutdown; everything else
				// delegates to RenderAppLogsError so the
				// NotFound / Unavailable envelopes stay
				// the source of truth (not open-coded
				// here).
				if errors.Is(r.err, io.EOF) {
					_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
					if flusher != nil {
						flusher.Flush()
					}
					return
				}
				apislogs.RenderAppLogsError(w, flusher, r.err)
				return
			}
			// Gap vs line (issue #517 / PR-B, AC4): schedd forwards
			// synthetic gap frames whenever a vmmd's ring no longer
			// retains the cursor the caller asked for. Render the
			// matching SSE envelope; the stream continues after a
			// gap with the surviving replay and the live tail.
			if r.frame.IsGap {
				apislogs.LogAppLogFrame(h.Log, r.frame, accountID, appIdentity, deploymentID)
				apislogs.RenderAppLogGap(w, flusher, r.frame, appID, h.Ops)
				continue
			}
			apislogs.LogAppLogFrame(h.Log, r.frame, accountID, appIdentity, deploymentID)
			apislogs.RenderAppLogEvent(w, flusher, r.frame, appID, h.Ops)
		}
	}
}

// recvResult carries one upstream delivery on the receive
// channel from serveAppLogs's receive goroutine. Exactly one
// of frame or err is meaningful per result. The receive
// goroutine closes the channel on its way out so the handler
// can treat a missing result as a clean EOF and emit the
// terminal `event: end {}` sentinel.
type recvResult struct {
	frame scheddgrpc.LogFrame
	err   error
}

// enforceDeploymentFilter writes the
// plan_deployment_filter_not_allowed SSE error frame and returns
// false when the customer's plan is not allowed to scope logs by
// deployment. Returns true (no write, no early-out) when the
// customer is on Hobby/Pro/Scale (whose per-plan cap is > 0) and
// must therefore not be gated.
//
// Extracted from stream() so the plan-gate rule has its own
// whitebox coverage (TestServeAppLogs_FreeRejectsDeploymentFilter
// + the Hobby-allowed sibling) without standing up the full Auth
// + Store chain — same whitebox-seam pattern as
// runServeAppLogs/safeBuffer elsewhere in this package.
func enforceDeploymentFilter(w http.ResponseWriter, plan api.Plan) bool {
	max := plan.LogDeploymentFilterMax()
	if max > 0 {
		return true
	}
	apislogs.StartSSE(w)
	flusher, _ := w.(http.Flusher)
	apislogs.WritePlanDeploymentFilterNotAllowedError(w, flusher, max)
	return false
}
