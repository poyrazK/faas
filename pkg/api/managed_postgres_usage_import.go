package api

import "time"

type ManagedPostgresUsageImportReading struct {
	Meter    string `json:"meter"`
	Quantity int64  `json:"quantity"`
}

type ManagedPostgresUsageImportWindow struct {
	From       time.Time                           `json:"from"`
	To         time.Time                           `json:"to"`
	ObservedAt time.Time                           `json:"observed_at"`
	Readings   []ManagedPostgresUsageImportReading `json:"readings"`
}

type ManagedPostgresUsageImportRequest struct {
	ImportID          string                             `json:"import_id"`
	DatabaseID        string                             `json:"database_id"`
	EvidenceReference string                             `json:"evidence_reference"`
	EvidenceSHA256    string                             `json:"evidence_sha256"`
	Reason            string                             `json:"reason"`
	Windows           []ManagedPostgresUsageImportWindow `json:"windows"`
	ExpectedRevision  string                             `json:"expected_revision,omitempty"`
}

type ManagedPostgresUsageImportResult struct {
	ImportID               string    `json:"import_id"`
	DatabaseID             string    `json:"database_id"`
	Revision               string    `json:"revision"`
	Applied                bool      `json:"applied"`
	WindowCount            int       `json:"window_count"`
	PreviousCostMillicents int64     `json:"previous_cost_millicents"`
	ImportedCostMillicents int64     `json:"imported_cost_millicents"`
	CostDeltaMillicents    int64     `json:"cost_delta_millicents"`
	CollectedFrom          time.Time `json:"collected_from"`
	CollectedUntil         time.Time `json:"collected_until"`
	ObservedAt             time.Time `json:"observed_at"`
}
