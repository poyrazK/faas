package api

import "time"

// ObsAlertTriageHistory contains revision-bounded receipts for one episode.
// Later pages preserve SnapshotRevision but can observe audit retention changes.
type ObsAlertTriageHistory struct {
	IncidentID       string                        `json:"incident_id"`
	GeneratedAt      time.Time                     `json:"generated_at"`
	SnapshotAt       time.Time                     `json:"snapshot_at"`
	SnapshotRevision int64                         `json:"snapshot_revision"`
	CurrentRevision  int64                         `json:"current_revision"`
	Items            []ObsAlertTriageHistoryEntry  `json:"items"`
	Limit            int                           `json:"limit"`
	Truncated        bool                          `json:"truncated"`
	NextCursor       string                        `json:"next_cursor"`
	HistoryPath      string                        `json:"history_path"`
	Coverage         ObsAlertTriageHistoryCoverage `json:"coverage"`
}

// ObsAlertTriageHistoryEntry excludes actor identities and raw audit payloads.
// Workflow flags are the values immediately after the recorded action.
type ObsAlertTriageHistoryEntry struct {
	ID           string     `json:"id"`
	Revision     int64      `json:"revision"`
	Action       string     `json:"action"`
	At           time.Time  `json:"at"`
	Acknowledged bool       `json:"acknowledged"`
	Owned        bool       `json:"owned"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	TraceID      string     `json:"trace_id,omitempty"`
	TracePath    string     `json:"trace_path,omitempty"`
}

// Coverage counts retained receipts across the snapshot revision range, not
// just the page. Missing receipts do not establish the cause of their absence.
type ObsAlertTriageHistoryCoverage struct {
	State                   string `json:"state"`
	Reason                  string `json:"reason,omitempty"`
	AvailableEntries        int64  `json:"available_entries"`
	MissingEntries          int64  `json:"missing_entries"`
	OldestAvailableRevision *int64 `json:"oldest_available_revision,omitempty"`
	NewestAvailableRevision *int64 `json:"newest_available_revision,omitempty"`
}
