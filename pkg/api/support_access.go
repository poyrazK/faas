package api

// Customer-granted support access (ADR-20261116163508): an organization's
// owners and admins let Gregale support deploy, roll back and restart its
// apps for a limited time.

// SupportGrantHeader names the grant a support request acts under. Without
// it an operator is an ordinary account with no access to customer apps.
const SupportGrantHeader = "Support-Grant"

// Support grant statuses on the wire.
const (
	SupportGrantActive  = "active"
	SupportGrantRevoked = "revoked"
	SupportGrantExpired = "expired"
)

// SupportGrant is one grant. Apps lists app slugs; empty means every app of
// the organization.
type SupportGrant struct {
	ID        string   `json:"id"`
	Org       string   `json:"org"`
	Apps      []string `json:"apps"`
	AllApps   bool     `json:"all_apps"`
	Status    string   `json:"status"`
	Reason    string   `json:"reason"`
	CreatedBy string   `json:"created_by"`
	RequestID string   `json:"request_id,omitempty"`
	CreatedAt string   `json:"created_at"`
	ExpiresAt string   `json:"expires_at"`
	RevokedAt string   `json:"revoked_at,omitempty"`
	RevokedBy string   `json:"revoked_by,omitempty"`
}

// SupportAccessRequest is support asking for a grant.
type SupportAccessRequest struct {
	ID        string `json:"id"`
	Org       string `json:"org"`
	Operator  string `json:"operator"`
	Reason    string `json:"reason"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	DecidedAt string `json:"decided_at,omitempty"`
}

// SupportAccess is GET /v1/orgs/{slug}/support-access.
type SupportAccess struct {
	Enabled  bool                   `json:"enabled"`
	Grants   []SupportGrant         `json:"grants"`
	Requests []SupportAccessRequest `json:"requests"`
}

// CreateSupportGrantRequest is the body that grants access or approves a
// request. Apps are app slugs; empty grants every app of the organization.
// TTLHours is 1 to 168; zero means 24.
type CreateSupportGrantRequest struct {
	Apps     []string `json:"apps,omitempty"`
	TTLHours int      `json:"ttl_hours,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// OperatorSupportAccessRequest is an operator asking an organization for
// access.
type OperatorSupportAccessRequest struct {
	Org    string `json:"org"`
	Reason string `json:"reason"`
}

// SupportGrantList is the operator's view of live grants.
type SupportGrantList struct {
	Grants []SupportGrant `json:"grants"`
}
