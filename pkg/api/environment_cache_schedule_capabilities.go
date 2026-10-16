package api

// EnvironmentCacheScheduleCapabilities describes serving API/backend support,
// not target authorization, migration health, or fleet-wide writer readiness.
type EnvironmentCacheScheduleCapabilities struct {
	Listing          bool  `json:"listing"`
	Activity         bool  `json:"activity"`
	Checkpoint       bool  `json:"checkpoint"`
	RetentionSeconds int64 `json:"retention_seconds"`
}

func (c *EnvironmentCacheScheduleCapabilities) SupportsRecovery() bool {
	return c != nil && c.Listing && c.Activity && c.Checkpoint && c.RetentionSeconds == EnvironmentCacheActivityRetentionSeconds
}
