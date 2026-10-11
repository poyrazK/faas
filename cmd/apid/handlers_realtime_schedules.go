package main

import (
	"encoding/base64"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
)

func realtimeScheduleResponse(row state.ManagedRealtimeSchedule) api.ManagedRealtimeScheduleResponse {
	return api.ManagedRealtimeScheduleResponse{ScheduleID: row.ID, Group: row.Group, Conditions: row.Conditions, OnConditionFailure: row.OnConditionFailure, SkippedOccurrences: row.SkippedOccurrences, SkipReason: row.SkipReason, IntervalSeconds: row.IntervalSeconds, MaxOccurrences: row.MaxOccurrences, EndAt: row.EndAt, InitialDeliverAt: row.InitialDeliverAt, Occurrence: row.Occurrence, CompletedOccurrences: row.CompletedOccurrences, MaxAttempts: row.MaxAttempts, BackoffSeconds: row.BackoffSeconds, Attempts: row.Attempts, CycleAttempts: row.CycleAttempts, NextAttemptAt: row.NextAttemptAt, LastAttemptAt: row.LastAttemptAt, Channel: row.Channel, DataBase64: base64.StdEncoding.EncodeToString(row.Data), Binary: row.Binary, Metadata: row.Metadata, DeliverAt: row.DeliverAt, Version: row.Version, Status: row.Status, Sequence: row.Sequence, LastError: row.LastError, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
func (s *server) managedRealtimeSchedules(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	store, ok := s.store.(state.ManagedRealtimeScheduleStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("scheduled realtime publishing unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var row state.ManagedRealtimeSchedule
	var err error
	switch r.Method {
	case http.MethodGet:
		if r.PathValue("schedule_id") != "" {
			historyStore, ok := s.store.(state.ManagedRealtimeScheduleHistoryStore)
			if !ok {
				api.WriteProblem(w, api.ErrCapacity("schedule history unavailable"))
				return
			}
			var after int64
			limit := 50
			query := r.URL.Query()
			valid := true
			if values, exists := query["after_version"]; exists {
				if len(values) != 1 {
					valid = false
				} else {
					after, err = strconv.ParseInt(values[0], 10, 64)
					valid = err == nil && after >= 0
				}
			}
			if values, exists := query["limit"]; exists {
				if len(values) != 1 {
					valid = false
				} else {
					var parseErr error
					limit, parseErr = strconv.Atoi(values[0])
					valid = valid && parseErr == nil && limit >= 1 && limit <= 100
				}
			}
			if !valid {
				api.WriteProblem(w, api.ErrRealtimeInvalid("history requires after_version >= 0 and limit from 1 to 100"))
				return
			}
			var history state.ManagedRealtimeScheduleHistory
			history, err = historyStore.ReadManagedRealtimeScheduleHistory(r.Context(), ep.ID, ch, r.PathValue("schedule_id"), after, limit)
			if err == nil {
				writeJSON(w, http.StatusOK, history)
				return
			}
			break
		}
		var rows []state.ManagedRealtimeSchedule
		rows, err = store.ListManagedRealtimeSchedules(r.Context(), ep.ID, ch)
		if err == nil {
			rows, err = filterRealtimeSchedules(r, rows)
			if err != nil {
				s.writeManagedRealtimeHistoryError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, realtimeScheduleListResponse(rows))
			return
		}
	case http.MethodPut:
		var req api.ManagedRealtimeScheduleRequest
		if decodeJSONSized(r, &req, 16<<10) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("schedule requires payload and deliver_at"))
			return
		}
		var data []byte
		data, err = base64.StdEncoding.DecodeString(req.DataBase64)
		if err != nil || len(data) > 4096 || ep.MaxMessageBytes > 0 && int64(len(data)) > ep.MaxMessageBytes {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid scheduled payload or endpoint size limit"))
			return
		}
		row, err = store.PutManagedRealtimeSchedule(r.Context(), state.ManagedRealtimeSchedule{EndpointID: ep.ID, Channel: ch, Group: req.Group, ID: r.PathValue("schedule_id"), Data: data, Binary: req.Binary, Metadata: req.Metadata, DeliverAt: req.DeliverAt, MaxAttempts: req.MaxAttempts, BackoffSeconds: req.BackoffSeconds, IntervalSeconds: req.IntervalSeconds, MaxOccurrences: req.MaxOccurrences, EndAt: req.EndAt, Conditions: req.Conditions, OnConditionFailure: req.OnConditionFailure})
	case http.MethodPost:
		if action := r.PathValue("recurrence_action"); action != "" {
			var req api.ManagedRealtimeSchedulePauseRequest
			if decodeJSONSized(r, &req, 1024) != nil || req.ExpectedVersion < 1 || (action != "pause" && action != "resume") {
				api.WriteProblem(w, api.ErrRealtimeInvalid("pause/resume requires expected_version"))
				return
			}
			recurrenceStore, ok := s.store.(state.ManagedRealtimeRecurrenceStore)
			if !ok {
				api.WriteProblem(w, api.ErrCapacity("recurrence management unavailable"))
				return
			}
			row, err = recurrenceStore.SetManagedRealtimeSchedulePaused(r.Context(), ep.ID, ch, r.PathValue("schedule_id"), req.ExpectedVersion, action == "pause")
			break
		}
		var req api.ManagedRealtimeScheduleRetryRequest
		if decodeJSONSized(r, &req, 1024) != nil || req.ExpectedVersion < 1 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("retry requires expected_version and an optional future deliver_at"))
			return
		}
		retryStore, ok := s.store.(state.ManagedRealtimeScheduleRetryStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("scheduled retry unavailable"))
			return
		}
		row, err = retryStore.RetryManagedRealtimeSchedule(r.Context(), ep.ID, ch, r.PathValue("schedule_id"), req.ExpectedVersion, req.DeliverAt)
	case http.MethodPatch:
		var req api.ManagedRealtimeScheduleUpdate
		if decodeJSONSized(r, &req, 1024) != nil || req.ExpectedVersion < 1 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("rescheduling requires deliver_at and expected_version"))
			return
		}
		row, err = store.UpdateManagedRealtimeSchedule(r.Context(), ep.ID, ch, r.PathValue("schedule_id"), req.ExpectedVersion, &req.DeliverAt)
	case http.MethodDelete:
		versions := r.URL.Query()["expected_version"]
		var version int64
		if len(versions) == 1 {
			version, err = strconv.ParseInt(versions[0], 10, 64)
		}
		if len(versions) != 1 || err != nil || version < 1 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("cancellation requires one positive expected_version"))
			return
		}
		row, err = store.UpdateManagedRealtimeSchedule(r.Context(), ep.ID, ch, r.PathValue("schedule_id"), version, nil)
	}
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "realtime schedule not found")
		return
	}
	if errors.Is(err, state.ErrManagedRealtimeScheduleLimit) {
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Schedule limit reached", "an endpoint can retain 256 pending or recent terminal schedules"))
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Realtime schedule conflict", "schedule ID has different contents, expected_version is stale, or schedule has an incompatible status"))
		return
	}
	if err != nil {
		s.writeManagedRealtimeHistoryError(w, r, err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.schedule_updated", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": ch, "schedule_id": row.ID, "status": row.Status, "version": row.Version})
	writeJSON(w, http.StatusOK, realtimeScheduleResponse(row))
}
