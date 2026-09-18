package api

import "time"

// TCPListenerResponse is the customer-safe projection of an app-owned raw
// TCP listener. PublicPort is stable across instance wake and migration.
type TCPListenerResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	GuestPort  int       `json:"guest_port"`
	PublicPort int       `json:"public_port"`
	Protocol   string    `json:"protocol"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CreateTCPListenerRequest creates one app-owned raw TCP listener. PublicPort
// is optional; apid allocates one from Gregale's reserved public range when
// omitted. Listeners are enabled immediately after creation.
type CreateTCPListenerRequest struct {
	Name       string `json:"name"`
	GuestPort  int    `json:"guest_port"`
	PublicPort int    `json:"public_port,omitempty"`
}

// UpdateTCPListenerRequest changes the serving state without changing the
// listener identity or its stable public endpoint.
type UpdateTCPListenerRequest struct {
	Enabled *bool `json:"enabled"`
}
