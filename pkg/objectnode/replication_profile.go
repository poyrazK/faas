package objectnode

import "time"

// ReplicationProfile observes one bounded catch-up attempt, including failures.
// Phases are wall time and include their budget waits. Durable phases include
// metadata/copy work and fsync; they are not raw device latency measurements.
// Verification is a subset of DownloadMS; other phases do not overlap.
type ReplicationProfile struct {
	TotalMS        float64 `json:"total_ms"`
	SourceCheckMS  float64 `json:"source_check_ms"`
	EntryFetchMS   float64 `json:"entry_fetch_ms"`
	JournalMS      float64 `json:"durable_journal_ms"`
	DownloadMS     float64 `json:"download_ms"`
	VerificationMS float64 `json:"download_verification_ms"`
	ApplyMS        float64 `json:"durable_apply_ms"`
	CheckpointMS   float64 `json:"durable_checkpoint_ms"`
	ReportMS       float64 `json:"report_ms"`
	ByteWaitMS     float64 `json:"byte_budget_wait_ms"`
	EntryWaitMS    float64 `json:"entry_budget_wait_ms"`
}

func addReplicaTime(target *float64, started time.Time) {
	*target += float64(time.Since(started)) / float64(time.Millisecond)
}
