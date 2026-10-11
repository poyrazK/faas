package api

import "time"

// Retry requires a fresh inspection; these comparison fields grant no authority.
type DurableEntityRetryRequest struct {
	Namespace                string     `json:"namespace"`
	Key                      string     `json:"key"`
	Environment              string     `json:"environment,omitempty"`
	PlatformTenantID         string     `json:"platform_tenant_id,omitempty"`
	Target                   string     `json:"target"`
	ExpectedVersion          uint64     `json:"expected_version"`
	ExpectedRecoveryRevision string     `json:"expected_recovery_revision"`
	HeadID                   string     `json:"head_id,omitempty"`
	AlarmAt                  *time.Time `json:"alarm_at,omitempty"`
}

type DurableEntityRetryResponse struct {
	Version uint64 `json:"version"`
	Target  string `json:"target"`
	Rearmed bool   `json:"rearmed"`
}
