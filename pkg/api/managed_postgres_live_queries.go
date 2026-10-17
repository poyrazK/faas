package api

// ManagedPostgresLiveQueries is a managed database's live-query state
// (ADR-1384).
type ManagedPostgresLiveQueries struct {
	DatabaseID string                   `json:"database_id"`
	Enabled    bool                     `json:"enabled"`
	Keys       []ManagedPostgresLiveKey `json:"keys"`
	// Secret is the live secret (glsk1_...) that backends seal queries
	// with. Only the response that issues it carries it; it cannot be read
	// again.
	Secret string `json:"secret,omitempty"`
}

// ManagedPostgresLiveKey is one live secret, without its key material.
type ManagedPostgresLiveKey struct {
	KeyID     string `json:"key_id"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
	// RetiresAt is when a rotated-out secret stops opening seals.
	RetiresAt string `json:"retires_at,omitempty"`
}

// RotateManagedPostgresLiveSecretRequest rotates a database's live secret.
type RotateManagedPostgresLiveSecretRequest struct {
	// OverlapSeconds is how long the previous secret keeps opening seals.
	// Omitted means 24 hours; 0 revokes it at once.
	OverlapSeconds *int64 `json:"overlap_seconds,omitempty"`
}
