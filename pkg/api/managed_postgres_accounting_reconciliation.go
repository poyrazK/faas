package api

import "time"

type ManagedPostgresAccountingReconciliationRequest struct {
	ReconciliationID   string    `json:"reconciliation_id"`
	DatabaseID         string    `json:"database_id"`
	BackendID          string    `json:"backend_id"`
	BackendFingerprint string    `json:"backend_fingerprint"`
	ProviderResourceID string    `json:"provider_resource_id"`
	ShutdownAt         time.Time `json:"shutdown_at"`
	ObservedAt         time.Time `json:"observed_at"`
	EvidenceReference  string    `json:"evidence_reference"`
	EvidenceSHA256     string    `json:"evidence_sha256"`
	Reason             string    `json:"reason"`
	ExpectedRevision   string    `json:"expected_revision,omitempty"`
}

type ManagedPostgresAccountingReconciliationResult struct {
	ReconciliationID      string     `json:"reconciliation_id"`
	DatabaseID            string     `json:"database_id"`
	Revision              string     `json:"revision"`
	Applied               bool       `json:"applied"`
	PreviousDeletedAt     *time.Time `json:"previous_deleted_at,omitempty"`
	ShutdownAt            time.Time  `json:"shutdown_at"`
	ObservedAt            time.Time  `json:"observed_at"`
	SharedAccounting      bool       `json:"shared_accounting"`
	RequiresUsageRecovery bool       `json:"requires_usage_recovery"`
}
