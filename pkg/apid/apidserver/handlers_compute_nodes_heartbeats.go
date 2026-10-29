package apidserver

// CP-1: GET /v1/compute-nodes/{name}/heartbeats. Operator-facing
// read-only surface that surfaces the schedd heartbeat history. The
// schedd Heartbeat.Tick goroutine writes one row per successful
// ping (CP-1, pkg/sched/heartbeat.go step 230+); this handler
// reads from the same table.
//
// Auth chain: authLimited → requireMFA → requireScope(ScopesAdminOnly)
// → adminAllows (in handler). The first three are mounted at
// server.go (~line 773); adminAllows is the existing in-handler
// email allowlist gate used by the rest of /v1/compute-nodes.
//
// Wire shape: computeNodeHeartbeatsResponse. The `missed` / `stale`
// flags come from state.ClassifyHeartbeatGap (the same oracle the
// property test in pkg/sched/heartbeat_gap_test.go pins; the
// classifier is re-exported from pkg/state because apid's depguard
// rejects imports from pkg/sched — spec §Component ownership).

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// heartbeatDefaultSinceWindow is the default lower bound when
	// the operator omits ?since=. 30 minutes covers the routine
	// "what did this box look like lately" question without forcing
	// the operator to compute a timestamp.
	heartbeatDefaultSinceWindow = 30 * time.Minute
	// heartbeatMaxSinceWindow is the hard cap on ?since=. A 24h
	// window is the largest operator-friendly view; anything older
	// belongs in a database query, not a JSON response.
	heartbeatMaxSinceWindow = 24 * time.Hour
	// heartbeatDefaultLimit is the default row cap when the
	// operator omits ?limit=. 30s × 200 = ~100 minutes of history.
	heartbeatDefaultLimit = 200
	// heartbeatMaxLimit is the hard cap on ?limit=. 30s × 2000 =
	// ~16 hours; matches the "last 24h" intent.
	heartbeatMaxLimit = 2000
)

// computeNodeHeartbeatRow is one row in the response heartbeat
// array. The fields are server-side derived from the history row
// plus the gap to the previous row (driven by state.ClassifyHeartbeatGap).
type computeNodeHeartbeatRow struct {
	ReceivedAt      string `json:"received_at"`
	LastHeartbeatAt string `json:"last_heartbeat_at"`
	Source          string `json:"source"`
	GapToPreviousMS int64  `json:"gap_to_previous_ms"`
	Missed          bool   `json:"missed"`
	Stale           bool   `json:"stale"`
}

// computeNodeHeartbeatsResponse is the JSON wire shape for the
// /v1/compute-nodes/{name}/heartbeats endpoint. The `since` field
// is omitted when the operator didn't pass one (the handler
// defaults to heartbeatDefaultSinceWindow). `since_clamped` is
// `true` when the operator asked for an older window than the
// heartbeatMaxSinceWindow hard cap — the response reflects the
// clamped window, and the flag tells the operator the request was
// narrowed (F4: silent clamping is surprising; the operator
// debugging "did the box flap at 14:32 last Tuesday?" needs to
// see their query was capped at 24h, not just get a 24h response).
type computeNodeHeartbeatsResponse struct {
	NodeID       string                    `json:"node_id"`
	Name         string                    `json:"name"`
	Since        string                    `json:"since,omitempty"`
	SinceClamped bool                      `json:"since_clamped,omitempty"`
	Heartbeats   []computeNodeHeartbeatRow `json:"heartbeats"`
}

// listComputeNodeHeartbeats handles GET /v1/compute-nodes/{name}/heartbeats.
func (s *server) listComputeNodeHeartbeats(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if ok, prob := s.adminAllows(acct); !ok {
		api.WriteProblem(w, prob)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing name", "path parameter name is required"))
		return
	}
	node, err := s.store.ComputeNodeByName(r.Context(), name)
	if err != nil {
		s.notFound(w, "no such compute_node")
		return
	}
	since, clamped, prob := parseComputeNodeHeartbeatSince(r, time.Now())
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limit := parseComputeNodeHeartbeatLimit(r)
	rows, err := s.store.ListComputeNodeHeartbeats(r.Context(), node.ID, since, limit)
	if err != nil {
		logCustomerFailure(s.log, "List failed", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal",
			"List failed", api.DetailServerFailure))
		return
	}
	out := computeNodeHeartbeatsResponse{
		NodeID:       node.ID,
		Name:         node.Name,
		SinceClamped: clamped,
		Heartbeats:   make([]computeNodeHeartbeatRow, 0, len(rows)),
	}
	// Only emit the `since` field when the caller passed one
	// (so the wire shape is "what I asked for"). The default
	// window is internal to the handler and not surfaced.
	if seen := r.URL.Query().Get("since"); seen != "" {
		out.Since = since.UTC().Format("2006-01-02T15:04:05.999999Z07:00")
	}
	// The store returns newest-first (the SQL composite index is
	// `received_at DESC`; MemStore iterates in reverse insertion
	// order). The wire shape's `missed` / `stale` flags classify
	// each row against the row that came BEFORE it in time, so
	// the OLDEST row has no baseline. Walk newest→oldest, then
	// emit oldest→newest so the operator sees row 0 as the
	// baseline and each subsequent row carries a positive gap to
	// its older neighbour.
	out.Heartbeats = make([]computeNodeHeartbeatRow, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		var summary state.HeartbeatGapSummary
		if i+1 < len(rows) {
			// rows[i+1] is OLDER than rows[i] in a newest-first
			// slice (it's at a higher index). Use it as the
			// classifier's "previous" so gap = curr - prev is
			// positive when the heartbeats are running on cadence.
			prev := rows[i+1].ReceivedAt
			summary = state.ClassifyHeartbeatGap(prev, row.ReceivedAt,
				state.DefaultHeartbeatInterval, state.DefaultHeartbeatStaleness)
		}
		out.Heartbeats = append(out.Heartbeats, computeNodeHeartbeatRow{
			ReceivedAt:      row.ReceivedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"),
			LastHeartbeatAt: row.LastHeartbeatAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00"),
			Source:          row.Source,
			GapToPreviousMS: summary.Gap.Milliseconds(),
			Missed:          summary.Missed,
			Stale:           summary.Stale,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// parseComputeNodeHeartbeatSince parses the ?since= query parameter
// and returns either the parsed time or an RFC 7807 problem written
// to the response. The default (no ?since=) is the past 30 minutes,
// clamped to a 24h hard cap. A future timestamp is a 400 — the
// operator is asking for hidden data.
//
// The second return is `clamped` — true when the operator passed a
// timestamp older than heartbeatMaxSinceWindow and the function
// silently narrowed the window to 24h. The handler surfaces this on
// the wire as `since_clamped: true` so the operator knows their
// query was capped (F4: silent clamping is surprising; an operator
// debugging a historical flap needs to see their query was narrowed,
// not get a silent 24h response).
func parseComputeNodeHeartbeatSince(r *http.Request, now time.Time) (time.Time, bool, *api.Problem) {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return now.Add(-heartbeatDefaultSinceWindow), false, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid since", `"since" must be an RFC 3339 timestamp (e.g. 2026-07-29T00:00:00Z)`)
	}
	if t.After(now) {
		return time.Time{}, false, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid since", `"since" must not be in the future`)
	}
	minSince := now.Add(-heartbeatMaxSinceWindow)
	if t.Before(minSince) {
		return minSince, true, nil
	}
	return t, false, nil
}

// parseComputeNodeHeartbeatLimit parses ?limit= and applies the
// documented bounds (default 200, hard cap 2000). A bad value
// (negative, non-numeric) silently falls back to the default
// rather than 400'ing — operators prefer "I typo'd, you gave me
// the safe default" over a problem detail they have to decode.
// This matches the existing ?limit= parsing convention in
// handlers_audit.go.
func parseComputeNodeHeartbeatLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return heartbeatDefaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		n = heartbeatDefaultLimit
	}
	if n > heartbeatMaxLimit {
		n = heartbeatMaxLimit
	}
	return n
}
