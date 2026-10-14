package api

import "time"

// MCPCatalogOAuthRenewal is a bounded exchange journal. Public projections expose
// only state and start time; the saved result contains sealed credentials.
type MCPCatalogOAuthRenewal struct {
	Outcome      string                         `json:"outcome,omitempty"`
	State        string                         `json:"state"`
	StartedAt    time.Time                      `json:"started_at"`
	ID           string                         `json:"private_id,omitempty"`
	SourceSHA256 string                         `json:"private_source_sha256,omitempty"`
	LeaseID      string                         `json:"private_lease_id,omitempty"`
	Result       *MCPCatalogObserverOAuthStatus `json:"private_result,omitempty"`
}
