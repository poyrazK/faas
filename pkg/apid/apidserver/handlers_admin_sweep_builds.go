// handlers_admin_sweep_builds.go — operator-side recovery primitive
// P2c (reclaim-stuck-build). The on-call engineer posts here when
// the builder fleet has rows stuck in 'running' longer than the
// build VM timeout (10 min, per pkg/api/limits.go) — typically
// because the builder microVM crashed (OOM, kernel panic, host
// reboot) and the in-process reaper at pkg/builderd/reaper.go
// hasn't ticked yet, or because the operator wants to bypass the
// reaper's grace period for an incident.
//
// Per user decision: NO builderd gRPC server. apid calls
// state.Store.SweepStuckRunningBuilds directly — the method is
// already public and called by the reaper at
// pkg/builderd/reaper.go:48. The CAS guard on
// UpdateBuildStatus (issue #195 B1.4) closes the race against a
// completing build that flipped to 'failed/succeeded' between
// the SELECT and UPDATE inside the Store implementation.
//
// Auth + IDOR posture mirrors the other P2 primitives: admin
// scope + s.adminAllows (allowlist check) + ?confirm=true
// tripwire. The threshold param ?older_than= is bounded to
// [1m, 60m] to keep a fat-fingered "1ns" from sweeping every
// currently-running build. Default 15m mirrors the reaper's
// grace period. Every successful sweep also records the
// operator-supplied reason in the audit row.
//
// Audit row: operator.action.reclaim_build with
//
//	account_id = nil (fleet-level sweep, not tenant-scoped)
//	data = {actor: caller.ID, older_than_seconds, swept_count,
//	        threshold_iso, reason}
//
// matches the precedent at handlers_admin_obs.go:441 where
// fleet-level events stamp account_id=NULL.
package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// sweepDefaultOlderThan is the default ?older_than= duration
	// when the operator omits the param. Mirrors the reaper's
	// grace period at pkg/builderd/reaper.go (the reaper sweeps
	// at the build VM timeout, which is 10 min per
	// pkg/api/limits.go; the operator-facing sweep uses 15m so
	// a "1m" mistype doesn't sweep an in-flight build).
	sweepDefaultOlderThan = 15 * time.Minute
	// sweepMinOlderThan is the floor on ?older_than=. Below 1m
	// a fat-fingered "1ns" would sweep every currently-running
	// build — clamp the floor so the operator has to make an
	// intentional choice to drop below 1m.
	sweepMinOlderThan = 1 * time.Minute
	// sweepMaxOlderThan is the ceiling on ?older_than=. The
	// reaper grace period is 10m and the build VM timeout is
	// 10m; 60m is the operator's "find anything older than an
	// hour" investigation tool, beyond which the data is
	// almost certainly noise.
	sweepMaxOlderThan = 60 * time.Minute
)

// postSweepStuckBuilds handles POST /v1/admin/builds/sweep-stuck.
// 200 on success, 400 on missing ?confirm=true or invalid
// ?older_than=, 403 admin_required, 500 on store error.
func (s *server) postSweepStuckBuilds(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"confirm required",
			"?confirm=true is required to sweep stuck builds; aborts on operator typo"))
		return
	}
	reason, perr := parseSweepReason(r.URL.Query().Get("reason"))
	if perr != nil {
		api.WriteProblem(w, perr)
		return
	}
	olderThan, perr := parseSweepOlderThan(r.URL.Query().Get("older_than"))
	if perr != nil {
		api.WriteProblem(w, perr)
		return
	}

	threshold := time.Now().Add(-olderThan)
	swept, err := s.store.SweepStuckRunningBuilds(r.Context(), threshold)
	if err != nil {
		logCustomerFailure(s.log, "sweep failed", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "sweep failed", api.DetailServerFailure))
		return
	}
	emitOperatorActionReclaimBuild(r, s, acct, int(olderThan.Seconds()), swept, threshold.UTC().Format(time.RFC3339), reason)
	writeJSON(w, http.StatusOK, api.SweepStuckBuildsResponse{
		OK:            true,
		SweptCount:    swept,
		OlderThanSecs: int(olderThan.Seconds()),
		ThresholdISO:  threshold.UTC().Format(time.RFC3339),
	})
}

const sweepDefaultReason = "operator_reclaim_build"

// parseSweepReason returns the normalized operator reason or a 400 api.Problem.
// Reasons intentionally use the same constrained shape as the other operator
// lifecycle actions so audit search and incident tooling can treat all action
// records consistently.
func parseSweepReason(raw string) (string, *api.Problem) {
	if raw == "" {
		return sweepDefaultReason, nil
	}
	if len(raw) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(raw) {
		return "", api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid reason", "reason must match [a-z0-9_]{1,64}")
	}
	return raw, nil
}

// parseSweepOlderThan returns the ?older_than= duration parsed
// against the [1m, 60m] clamp, or a 400 api.Problem ready to
// write. Empty input returns the default (15m). The function
// is split out so the handler body stays under the 50-line
// limit and the validation surface is unit-testable.
//
// Problem codes: the operator's on-call dashboard filters on
// these to distinguish "the operator fat-fingered" from
// "the threshold is wrong for this incident". Custom codes
// are preferred over CodeValidation so a single failed
// request can be triaged from the audit row alone.
func parseSweepOlderThan(raw string) (time.Duration, *api.Problem) {
	if raw == "" {
		return sweepDefaultOlderThan, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, api.NewProblem(http.StatusBadRequest,
			"invalid_older_than",
			"invalid older_than",
			"?older_than must be a Go duration (e.g. 15m, 1h); got: "+raw)
	}
	if d < sweepMinOlderThan {
		return 0, api.NewProblem(http.StatusBadRequest,
			"older_too_small",
			"older_too_small",
			"?older_than must be >= 1m to avoid sweeping in-flight builds")
	}
	if d > sweepMaxOlderThan {
		return 0, api.NewProblem(http.StatusBadRequest,
			"older_too_large",
			"older_too_large",
			"?older_than must be <= 60m (the build VM timeout is 10m; beyond 1h the data is noise)")
	}
	return d, nil
}
