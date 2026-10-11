package api

import "time"

type DurableEntityBackup struct {
	CapturedAt time.Time                `json:"captured_at"`
	Export     DurableEntityStateExport `json:"export"`
}
type DurableEntityBackupInfo struct {
	ID         string    `json:"id"`
	CapturedAt time.Time `json:"captured_at"`
	Version    uint64    `json:"version"`
}
type DurableEntityBackupPage struct {
	Items      []DurableEntityBackupInfo `json:"items"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}
type DurableEntityRestorePreview struct {
	CurrentVersion         uint64  `json:"current_version"`
	SourceVersion          uint64  `json:"source_version"`
	ExpectedVersionMatches bool    `json:"expected_version_matches"`
	CurrentSchemaVersion   *uint32 `json:"current_schema_version,omitempty"`
	SourceSchemaVersion    *uint32 `json:"source_schema_version,omitempty"`
	SchemaRelation         string  `json:"schema_relation"`
	Compatibility          string  `json:"compatibility"`
	AlarmPending           bool    `json:"alarm_pending"`
	OutboxPending          int     `json:"outbox_pending"`
	AlarmExhausted         bool    `json:"alarm_exhausted"`
	OutboxExhausted        bool    `json:"outbox_exhausted"`
}
