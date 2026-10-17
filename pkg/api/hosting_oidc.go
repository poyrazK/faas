package api

import "time"

// Trust bindings are immutable. Replace a binding by revoking it and creating
// another; issued credentials are removed with the revoked binding.
type HostingOIDCTrustRules struct {
	RepositoryOwnerID string `json:"repository_owner_id"`
	RepositoryID      string `json:"repository_id"`
	Repository        string `json:"repository"`
	Ref               string `json:"ref"`
	WorkflowRef       string `json:"workflow_ref"`
	EventName         string `json:"event_name"`
	Environment       string `json:"environment,omitempty"`
}

type HostingOIDCTrust struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	SiteID    string `json:"site_id"`
	HostingOIDCTrustRules
	CreatedAt time.Time `json:"created_at"`
}

func HostingOIDCAudience(site string) string { return "gregale:hosting:" + site }

type HostingOIDCExchangeRequest struct {
	SiteID    string `json:"site_id"`
	TrustID   string `json:"trust_id"`
	Assertion string `json:"assertion"`
}
