package api

import "time"

// ManagedPostgresAccountingDiagnostic exposes local evidence to operators.
// Opaque provider identities and credential material are never serialized.
type ManagedPostgresAccountingDiagnostic struct {
	DatabaseID             string     `json:"database_id"`
	Name                   string     `json:"name"`
	State                  string     `json:"state"`
	AccountingRequired     bool       `json:"accounting_required"`
	IdentityKnown          bool       `json:"identity_known"`
	AccountingDatabaseID   string     `json:"accounting_database_id"`
	Blocking               bool       `json:"blocking"`
	Reasons                []string   `json:"reasons"`
	RequiredFrom           *time.Time `json:"required_from,omitempty"`
	RequiredUntil          *time.Time `json:"required_until,omitempty"`
	CollectedWindowSeconds int64      `json:"collected_window_seconds"`
	CollectedFrom          *time.Time `json:"collected_from,omitempty"`
	CollectedUntil         *time.Time `json:"collected_until,omitempty"`
	ObservedAt             *time.Time `json:"observed_at,omitempty"`
	CorrectionObservedAt   *time.Time `json:"correction_observed_at,omitempty"`
	CorrectionRequiredAt   *time.Time `json:"correction_required_at,omitempty"`
	LeaseUntil             *time.Time `json:"lease_until,omitempty"`
}

type ManagedPostgresAccountingDiagnosticsResponse struct {
	AccountID     string                                `json:"account_id"`
	EvaluatedAt   time.Time                             `json:"evaluated_at"`
	PolicyEnabled bool                                  `json:"policy_enabled"`
	WindowSeconds int64                                 `json:"window_seconds"`
	Items         []ManagedPostgresAccountingDiagnostic `json:"items"`
	NextCursor    string                                `json:"next_cursor,omitempty"`
}
