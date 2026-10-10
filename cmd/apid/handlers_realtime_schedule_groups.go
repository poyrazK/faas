package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func realtimeScheduleListResponse(rows []state.ManagedRealtimeSchedule) api.ManagedRealtimeSchedulesResponse {
	out := api.ManagedRealtimeSchedulesResponse{Schedules: make([]api.ManagedRealtimeScheduleResponse, 0, len(rows))}
	for _, row := range rows {
		out.Schedules = append(out.Schedules, realtimeScheduleResponse(row))
		switch row.Status {
		case "pending":
			out.Totals.Pending++
		case "paused":
			out.Totals.Paused++
		case "published":
			out.Totals.Published++
		case "failed":
			out.Totals.Failed++
		case "skipped":
			out.Totals.Skipped++
		case "canceled":
			out.Totals.Canceled++
		}
		out.Totals.CompletedOccurrences += row.CompletedOccurrences
		if row.IntervalSeconds == 0 && row.Status == "published" {
			out.Totals.CompletedOccurrences++
		}
		out.Totals.SkippedOccurrences += row.SkippedOccurrences
	}
	return out
}
func filterRealtimeSchedules(r *http.Request, rows []state.ManagedRealtimeSchedule) ([]state.ManagedRealtimeSchedule, error) {
	query := r.URL.Query()
	group, status := "", ""
	if values, ok := query["group"]; ok {
		if len(values) != 1 || values[0] == "" || len(values[0]) > 128 || strings.TrimSpace(values[0]) != values[0] || strings.ContainsAny(values[0], "\x00\r\n") {
			return nil, state.ErrManagedRealtimeHistoryInvalid
		}
		group = values[0]
	}
	if values, ok := query["status"]; ok {
		if len(values) != 1 {
			return nil, state.ErrManagedRealtimeHistoryInvalid
		}
		status = values[0]
		switch status {
		case "pending", "paused", "published", "failed", "skipped", "canceled":
		default:
			return nil, state.ErrManagedRealtimeHistoryInvalid
		}
	}
	out := make([]state.ManagedRealtimeSchedule, 0, len(rows))
	for _, row := range rows {
		if (group == "" || row.Group == group) && (status == "" || row.Status == status) {
			out = append(out, row)
		}
	}
	return out, nil
}
func (s *server) managedRealtimeScheduleGroup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "scheduled realtime publishing preview unavailable")
		return
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	ch := r.PathValue("channel")
	if problem := validateManagedRealtimeChannel(ch); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.ManagedRealtimeScheduleGroupStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("schedule group management unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var req api.ManagedRealtimeScheduleGroupRequest
	if decodeJSONSized(r, &req, 64<<10) != nil || req.ExpectedVersions == nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("group actions require expected_versions for every pending or paused member"))
		return
	}
	rows, err := store.ApplyManagedRealtimeScheduleGroup(r.Context(), ep.ID, ch, r.PathValue("group"), r.PathValue("group_action"), req.ExpectedVersions)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "realtime endpoint not found")
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Realtime schedule group conflict", "group membership or versions changed, or a member cannot transition; list the group again"))
		return
	}
	if err != nil {
		s.writeManagedRealtimeHistoryError(w, r, err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.schedule_group_updated", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": ch, "group": r.PathValue("group"), "action": r.PathValue("group_action"), "members": len(rows)})
	writeJSON(w, http.StatusOK, realtimeScheduleListResponse(rows))
}
