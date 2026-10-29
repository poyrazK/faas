package api

import (
	"encoding/json"
	"time"
)

// ObsWakeTimeline is GET /v1/admin/obs/wakes/{wake_id}: every platform event
// recorded for one wake, oldest first. An operator holding only a wake_id
// (from a log line, an exemplar or a customer report) reads the wake's phases
// without knowing the app or account. gregalectl obs wake merges these events
// with the wake's log lines from Loki into one timeline.
type ObsWakeTimeline struct {
	WakeID string `json:"wake_id"`
	// Subject is the first subject the events name (the app), when any does.
	Subject   string         `json:"subject,omitempty"`
	Events    []ObsWakeEvent `json:"events"`
	Truncated bool           `json:"truncated"`
}

// ObsWakeEvent is one events row of a wake.
type ObsWakeEvent struct {
	At      time.Time       `json:"at"`
	Kind    string          `json:"kind"`
	Actor   string          `json:"actor,omitempty"`
	Subject string          `json:"subject,omitempty"`
	TraceID string          `json:"trace_id,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}
