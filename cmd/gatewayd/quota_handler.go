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
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// internalQuotaHandler returns an http.HandlerFunc that reads the
// per-app rate-limit bucket for the (plan, app_id) query pair. h is
// the public-listener Handler (so the same Limiter instance is used;
// the snapshot reads from THIS gatewayd process's bucket). logger
// is the access-log sink; pass nil to disable access logging (tests).
func internalQuotaHandler(h *gateway.Handler, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if logger != nil {
			logger.Debug("gatewayd internal quota poll", "remote", r.RemoteAddr, "query", r.URL.RawQuery)
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
			w.Header().Set("X-Faas-Quota-State", "noop")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"app_id":"` + appID + `","plan":"` + planStr +
				`","limit":0,"remaining":0,"reset_seconds":0,"ok":false}`))
			return
		}
		w.Header().Set("X-Faas-Quota-State", "ok")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"app_id":"` + appID + `","plan":"` + planStr +
			`","limit":` + itoa(snap.Limit) +
			`,"remaining":` + itoa(snap.Remaining) +
			`,"reset_seconds":` + itoa(snap.ResetSeconds) +
			`,"ok":true}`))
	}
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

// itoa is the gatewayd-local int-to-string helper. There is one in
// pkg/gateway/handler.go already (used by the response-header writers);
// we can't import it without exposing it across packages. Keep this
// private duplication — the change is small enough that drift is
// caught by gofmt + the test that renders the JSON.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
