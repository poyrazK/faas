package main

import (
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"time"
)

func (s *server) loadRealtimeInspectorSchedules(r *http.Request, data *dashboard.RealtimeInspectorData) {
	store, ok := s.store.(state.ManagedRealtimeScheduleStore)
	if !ok {
		data.ScheduleError = "Scheduled publishing is unavailable."
		return
	}
	rows, err := store.ListManagedRealtimeSchedules(r.Context(), data.EndpointID, data.Channel)
	if err != nil {
		data.ScheduleError = "Scheduled events are temporarily unavailable."
		return
	}
	data.ScheduleTotal = len(rows)
	for _, row := range rows {
		switch row.Status {
		case "pending":
			data.PendingSchedules++
		case "paused":
			data.PausedSchedules++
		case "failed":
			data.FailedSchedules++
		}
		item := dashboard.RealtimeInspectorSchedule{ID: row.ID, Status: row.Status, Group: row.Group, DeliverAt: row.DeliverAt.UTC().Format(time.RFC3339Nano), LastError: row.LastError, Version: row.Version, Attempts: row.Attempts, Occurrence: row.Occurrence, Sequence: row.Sequence, IntervalSeconds: row.IntervalSeconds}
		if row.NextAttemptAt != nil {
			item.NextAttemptAt = row.NextAttemptAt.UTC().Format(time.RFC3339Nano)
		}
		data.Schedules = append(data.Schedules, item)
	}
}
