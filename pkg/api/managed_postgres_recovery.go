package api

// ManagedPostgresRecoveryStatus reports a live metadata observation. Necessary
// limits alone do not guarantee recoverability of every timestamp in the range.
type ManagedPostgresRecoveryStatus struct {
	DatabaseID           string `json:"database_id"`
	Status               string `json:"status"`
	Fresh                bool   `json:"fresh"`
	HistoryBoundsKnown   bool   `json:"history_bounds_known"`
	RetentionSeconds     int64  `json:"retention_seconds"`
	CheckedAt            string `json:"checked_at,omitempty"`
	EarliestPossibleTime string `json:"earliest_possible_time,omitempty"`
	LatestPossibleTime   string `json:"latest_possible_time,omitempty"`
	LastErrorCode        string `json:"last_error_code,omitempty"`
}
