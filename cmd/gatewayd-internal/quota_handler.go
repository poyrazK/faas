// /v1/internal/quota — Finding 6 (issue #314).
//
// Dashboard bucket-state endpoint. Mounted on the loopback-only
// control listener (default 127.0.0.1:9090, see pkg/gateway/control.go)
// so an in-box caller (currently the future apid-side dial; today an
// operator's curl) can read the per-app rate-limit bucket state
// without going through the public :443 listener where the request
// would self-rate-limit.
//
// Query:   GET /v1/internal/quota?plan=<plan>&app_id=<uuid>
//
//	plan is required (free|hobby|pro|scale); app_id is the
//	uuid the gateway stored when it Allow'd the bucket
//	(typically the apps.id row).
//
// Auth:    loopback bind only; no token. The control listener is
//
//	unauthenticated by design (operator-prometheus scrape is
//	the main consumer — see pkg/gateway/control.go). The
//	loopback bind is the auth. Running this on the public
//	listener would let a customer enumerate plan-by-plan.
//
// Response (JSON):
//
//	200   {"app_id":"...","plan":"...","limit":N,"remaining":N,
//	       "reset_seconds":N}    on known plan + observed bucket
//	200   same shape, ok=false  on unknown plan OR no observed
//	       bucket (callers must check both remaining and reset_seconds;
//	       the absent bucket case is signalled via reset_seconds>0
//	       which is impossible once reset_seconds is the
//	       "seconds-to-next-token" reading)
//	400   problem+json           on missing plan or app_id
//	503   problem+json + X-Faas-Quota-State: noop
//	                             on noop limiter (test / load-test
//	                             modes that bypass the throttle)
//
// Why this lives on the control listener, not the public one:
//   - The public listener self-rate-limits; a dashboard that polled
//     the public listener would 429 its own bucket.
//   - The control listener is loopback-only (spec §11 single-public-
//     listener invariant), so the only in-box callers are within the
//     same machine. Today no apid code path dials it; this handler
//     is the seam for a future dashboard wiring. Operating / v1
//     scripts that need the bucket state can curl it directly.
package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// internalQuotaHandler returns an http.HandlerFunc that reads the
// per-app rate-limit bucket for the (plan, app_id) query pair. h is
// the public-listener Handler (so the same Limiter instance is used;
// the snapshot reads from THIS gatewayd process's bucket). logger
// is the access-log sink; pass nil to disable access logging (tests).
func internalQuotaHandler(h *gateway.Handler, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if logger != nil {
			// r.URL.RawQuery is attacker-controllable — sanitize at the
			// source so a CR/LF in the query string can't break the
			// log-injection invariant (one log line per event).
			// r.RemoteAddr comes from the kernel, not the request, so
			// it doesn't need sanitizing. Precedent: cmd/gatewayd/
			// proxy.go:322 (apid proxy upstream error) and
			// githubd_proxy.go:142 (replay rejection).
			logger.Debug("gatewayd internal quota poll", "remote", r.RemoteAddr, "query", logsanitize.Field(r.URL.RawQuery))
		}
		q := r.URL.Query()
		planStr := q.Get("plan")
		appID := q.Get("app_id")
		if planStr == "" || appID == "" {
			writeProblemQuota(w, http.StatusBadRequest, "missing_param",
				"both plan and app_id query parameters are required")
			return
		}
		plan := quotaParsePlan(planStr)
		if !plan.Valid() {
			writeProblemQuota(w, http.StatusBadRequest, "unknown_plan",
				"plan must be one of free|hobby|pro|scale")
			return
		}
		if h == nil || h.Limiter() == nil {
			w.Header().Set("X-Faas-Quota-State", "noop")
			writeProblemQuota(w, http.StatusServiceUnavailable, "quota_unavailable",
				"rate limiter is not wired in this build")
			return
		}
		snap := h.QuotaSnapshot(appID, plan)
		w.Header().Set("Content-Type", "application/json")
		if !snap.OK {
			// ok=false: either the limiter is noop, the plan is
			// unknown, or the bucket has never been Allow'd. The
			// dashboard renders "—" on this case; the JSON shape is
			// still well-formed so the browser JS doesn't crash.
			//
			// Body built via json.Marshal so user-supplied
			// app_id/planStr are escaped (U+0022 / U+005C / control
			// bytes per RFC 8259 §7). The string-concat shape that
			// lived here through PR #314 was flagging CodeQL
			// go/reflected-xss (alert #146) — the endpoint is
			// loopback-only per assertLoopbackBind so the alert was
			// a FP for the live wire, but the concat was a real
			// defense-in-depth gap for any future caller that
			// renders the response as HTML. Marshal closes both.
			w.Header().Set("X-Faas-Quota-State", "noop")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(marshalQuotaSnapshot(quotaSnapshotJSON{
				AppID:        appID,
				Plan:         planStr,
				Limit:        0,
				Remaining:    0,
				ResetSeconds: 0,
				OK:           false,
			}))
			return
		}
		w.Header().Set("X-Faas-Quota-State", "ok")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(marshalQuotaSnapshot(quotaSnapshotJSON{
			AppID:        appID,
			Plan:         planStr,
			Limit:        snap.Limit,
			Remaining:    snap.Remaining,
			ResetSeconds: snap.ResetSeconds,
			OK:           true,
		}))
	}
}

// quotaSnapshotJSON is the wire shape returned by /v1/internal/quota.
// Field order is fixed so the JSON output matches the legacy
// string-concat shape (the dashboard JS parses both shapes; reordering
// would break the existing field name bindings in the dashboard).
type quotaSnapshotJSON struct {
	AppID        string `json:"app_id"`
	Plan         string `json:"plan"`
	Limit        int    `json:"limit"`
	Remaining    int    `json:"remaining"`
	ResetSeconds int    `json:"reset_seconds"`
	OK           bool   `json:"ok"`
}

// marshalQuotaSnapshot is the JSON encoder for the /v1/internal/quota
// response body. Uses encoding/json so user-supplied plan/app_id
// values are escaped per RFC 8259 §7 — patch for CodeQL alert #146
// (go/reflected-xss). The compiler can prove the return is a
// non-error []byte (no Marshaler implementations on the struct
// fields), so the error is intentionally dropped.
func marshalQuotaSnapshot(s quotaSnapshotJSON) []byte {
	b, _ := json.Marshal(s)
	return b
}

func writeProblemQuota(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	api.WriteProblem(w, api.NewProblem(status, code, "Quota check failed", detail))
}

// quotaParsePlan parses a plan name from a URL query string. Lives
// here (not in pkg/api) because pkg/api already has Plan.Valid for
// validation but no ParsePlan — adding ParsePlan to pkg/api widens
// the public surface for a single caller. Keep the validator copy
// local; duplicate the closed vocabulary inline so a typo in the
// allowed list is caught by unit-test enumeration.
func quotaParsePlan(s string) api.Plan {
	switch s {
	case "free":
		return api.PlanFree
	case "hobby":
		return api.PlanHobby
	case "pro":
		return api.PlanPro
	case "scale":
		return api.PlanScale
	}
	return api.Plan("")
}
