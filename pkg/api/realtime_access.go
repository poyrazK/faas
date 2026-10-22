package api

import "time"

// ManagedRealtimeAccessRequest changes durable controls for one authenticated
// principal. Restoring a channel removes only the platform deny; the application
// still authorizes new subscriptions.
type ManagedRealtimeAccessRequest struct {
	Action  string `json:"action"`
	Channel string `json:"channel,omitempty"`
}

type ManagedRealtimeAccessResponse struct {
	DeniedChannels        []string   `json:"denied_channels"`
	SessionsRevokedBefore *time.Time `json:"sessions_revoked_before,omitempty"`
	Revision              int64      `json:"revision"`
	UpdatedAt             time.Time  `json:"updated_at"`
}
