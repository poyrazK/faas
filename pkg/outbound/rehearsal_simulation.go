// adr: 1350
package outbound

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/onebox-faas/faas/pkg/api"
)

// SimulationStore serializes admission and completion with action sealing.
// This handler has no HTTP client, resolver, provider credentials, or fallback.
type SimulationStore interface {
	BeginScenarioSimulation(context.Context, api.ScenarioSimulationRequest) (api.ScenarioSimulationDecision, error)
	FinishScenarioSimulation(context.Context, api.ScenarioSimulationRequest, string, string) error
}

type SimulationHandler struct{ Store SimulationStore }

// WithSimulationRelay routes before the ordinary URL-tracing middleware.
// Refused query values and arbitrary guest headers must not be copied to spans.
// Simulation spans deliberately contain no request URL or header attributes.
func WithSimulationRelay(live, simulation http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := relayPrincipalFromContext(r.Context()); ok && p.rehearsalRunID != "" {
			if simulation == nil {
				writeProblem(w, http.StatusServiceUnavailable, "rehearsal_simulation_unavailable", "Rehearsal simulation unavailable", "")
				return
			}
			ctx, span := otel.Tracer("gregale/outbound").Start(r.Context(), "outboundd-rehearsal")
			defer span.End()
			simulation.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		live.ServeHTTP(w, r)
	})
}

func (h *SimulationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := relayPrincipalFromContext(r.Context())
	if !ok || principal.rehearsalRunID == "" || principal.execution != nil || h.Store == nil {
		writeProblem(w, http.StatusForbidden, "rehearsal_simulation_identity_required", "Simulation requires a host-authenticated rehearsal relay", "")
		return
	}
	req := readSimulationRequest(w, r, principal)
	decision, err := h.Store.BeginScenarioSimulation(r.Context(), req)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "rehearsal_simulation_unavailable", "Simulation admission or evidence storage unavailable", "")
		return
	}
	if decision.Attempt.Status != "pending" {
		writeProblem(w, http.StatusUnprocessableEntity, "rehearsal_simulation_unsupported", "Request has no supported fixture; rehearsal evidence is incomplete", "")
		return
	}
	status := "completed"
	if !waitSimulationResponse(r.Context(), decision.Response.DelayMS) {
		status = "interrupted"
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	err = h.Store.FinishScenarioSimulation(finishCtx, req, decision.Attempt.ID, status)
	cancel()
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "rehearsal_simulation_evidence_unavailable", "Simulation could not persist its completion", "")
		return
	}
	if status == "interrupted" || decision.Response.Fault != "" {
		panic(http.ErrAbortHandler)
	}
	if decision.WorkerFaultID != "" {
		w.Header().Set("X-Gregale-Rehearsal-Worker-Fault", decision.WorkerFaultID)
	}
	if decision.SuspendResumeID != "" {
		w.Header().Set("X-Gregale-Rehearsal-Suspend", decision.SuspendResumeID)
	}
	writeSimulationResponse(w, decision.Response)
}

func readSimulationRequest(w http.ResponseWriter, r *http.Request, p relayPrincipal) api.ScenarioSimulationRequest {
	req := api.ScenarioSimulationRequest{RecorderRequestID: p.recorderRequestID, AccountID: p.app.AccountID, AppID: p.app.AppID, InstanceID: p.app.InstanceID, RunID: p.rehearsalRunID, Method: r.Method}
	req.Integration, req.Path, _ = parsePath(r.URL.EscapedPath())
	req.IdempotencyKey = r.Header.Get("Idempotency-Key")
	req.Invalid = r.URL.RawQuery != "" || r.URL.IsAbs() || r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" || len(r.Header.Values("Idempotency-Key")) > 1
	// Bound stalled or oversized request bodies without depending on caller
	// cancellation. No header values or unrecognized paths enter the ledger.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	body, err := io.ReadAll(io.LimitReader(r.Body, api.MaxScenarioSimulationRequestBytes+1))
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	req.Body = body
	req.Invalid = req.Invalid || err != nil || len(body) > api.MaxScenarioSimulationRequestBytes || strings.Contains(req.Integration, "%")
	return req
}

func waitSimulationResponse(ctx context.Context, delayMS int) bool {
	if delayMS == 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(time.Duration(delayMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func writeSimulationResponse(w http.ResponseWriter, r api.ScenarioSimulationResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Gregale-Rehearsal", "simulated")
	if r.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(r.RetryAfterSeconds))
	}
	w.WriteHeader(r.Status)
	if r.Body != "" {
		_, _ = io.WriteString(w, r.Body)
	}
}

var _ http.Handler = (*SimulationHandler)(nil)
