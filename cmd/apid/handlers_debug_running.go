package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	debugRunningEventKind = "debug.running_reason"
	debugRunningLimitMax  = 100
)

// debugRunningEvent mirrors the scheduler's durable event shape. Keeping the
// reader tolerant of additional fields lets the scheduler add evidence without
// breaking older apid binaries during a rolling upgrade.
type debugRunningEvent struct {
	SchemaVersion          int                     `json:"schema_version"`
	AppID                  string                  `json:"app_id"`
	ObservedAt             string                  `json:"observed_at"`
	RunningInstances       int                     `json:"running_instances"`
	ConfiguredMinInstances int                     `json:"configured_min_instances"`
	EffectiveMinInstances  int                     `json:"effective_min_instances"`
	PrewarmMinInstances    int                     `json:"prewarm_min_instances,omitempty"`
	IdleTimeoutSeconds     int                     `json:"idle_timeout_seconds"`
	Degraded               bool                    `json:"degraded,omitempty"`
	Causes                 []api.DebugRunningCause `json:"causes"`
}

// debugRunningHandler serves the first customer-facing explanation of why an
// app stayed resident. It reads only app-subject scheduler observations and
// current app configuration; it never turns an absent signal into a claim
// that the app was idle or that a dollar amount would have changed.
func (s *server) debugRunningHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}

	sinceRaw := r.URL.Query().Get("since")
	since, err := parseDebugSinceStrict(sinceRaw, 24*time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	retentionClamped := false
	if retention > 0 && since > retention {
		since = retention
		retentionClamped = true
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil || n < 1 || n > debugRunningLimitMax {
			api.WriteProblem(w, api.ErrValidation("limit must be an integer between 1 and 100"))
			return
		}
		limit = n
	}

	now := time.Now().UTC()
	response, err := s.readDebugRunning(r.Context(), app, now.Add(-since), now, limit, limits.IdleTimeoutS)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list running debugger observations"))
		return
	}
	response.Since = echoDebugSince(sinceRaw, since)
	response.RetentionClamped = retentionClamped
	writeJSON(w, http.StatusOK, response)
}

func nonNilRunningCauses(causes []api.DebugRunningCause) []api.DebugRunningCause {
	if causes == nil {
		return []api.DebugRunningCause{}
	}
	return causes
}

// readDebugRunning returns the bounded scheduler observations used by both
// the public API and the server-rendered dashboard. The caller owns plan and
// window parsing; keeping the event projection here prevents the two customer
// surfaces from drifting apart during rolling upgrades.
func (s *server) readDebugRunning(ctx context.Context, app state.App, windowStart, windowEnd time.Time, limit, idleTimeoutSeconds int) (api.DebugRunningResponse, error) {
	if limit <= 0 {
		limit = 20
	}
	eventCap := limit * 4
	if eventCap < 40 {
		eventCap = 40
	}
	if eventCap > 400 {
		eventCap = 400
	}
	events, err := s.store.ListEvents(ctx, app.ID, eventCap+1)
	if err != nil {
		return api.DebugRunningResponse{}, err
	}

	history := make([]api.DebugRunningObservation, 0, limit)
	for _, row := range events {
		if row.Kind != debugRunningEventKind || row.At.Before(windowStart) || row.At.After(windowEnd.Add(time.Minute)) {
			continue
		}
		var event debugRunningEvent
		if err := json.Unmarshal(row.Data, &event); err != nil || event.AppID != "" && event.AppID != app.ID {
			continue
		}
		if event.ObservedAt == "" {
			event.ObservedAt = row.At.UTC().Format(time.RFC3339Nano)
		}
		history = append(history, api.DebugRunningObservation{
			EventID:                strconv.FormatInt(row.ID, 10),
			ObservedAt:             event.ObservedAt,
			RunningInstances:       event.RunningInstances,
			ConfiguredMinInstances: event.ConfiguredMinInstances,
			EffectiveMinInstances:  event.EffectiveMinInstances,
			PrewarmMinInstances:    event.PrewarmMinInstances,
			IdleTimeoutSeconds:     event.IdleTimeoutSeconds,
			Degraded:               event.Degraded,
			Causes:                 nonNilRunningCauses(event.Causes),
		})
		if len(history) == limit {
			break
		}
	}

	config := api.DebugRunningConfig{
		ConfiguredMinInstances: app.EffectiveMinInstances(),
		EffectiveMinInstances:  app.EffectiveMinInstances(),
		IdleTimeoutSeconds:     idleTimeoutSeconds,
	}
	var current []api.DebugRunningCause
	var currentObservedAt string
	if len(history) > 0 {
		config.ConfiguredMinInstances = history[0].ConfiguredMinInstances
		config.EffectiveMinInstances = history[0].EffectiveMinInstances
		config.PrewarmMinInstances = history[0].PrewarmMinInstances
		config.IdleTimeoutSeconds = history[0].IdleTimeoutSeconds
		current = history[0].Causes
		currentObservedAt = history[0].ObservedAt
	}
	if current == nil {
		current = []api.DebugRunningCause{}
	}
	return api.DebugRunningResponse{
		AppID:             app.ID,
		WindowStart:       windowStart.UTC().Format(time.RFC3339Nano),
		WindowEnd:         windowEnd.UTC().Format(time.RFC3339Nano),
		Current:           current,
		CurrentObservedAt: currentObservedAt,
		Config:            config,
		History:           history,
		HistoryTruncated:  len(history) == limit,
	}, nil
}
