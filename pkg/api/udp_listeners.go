package api

import "time"

// UDPListenerResponse is the customer-safe projection of an app-owned raw
// UDP listener. PublicPort is stable across instance wake and migration.
type UDPListenerResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	GuestPort  int       `json:"guest_port"`
	PublicPort int       `json:"public_port"`
	Protocol   string    `json:"protocol"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CreateUDPListenerRequest creates one app-owned raw UDP listener. PublicPort
// is optional; apid allocates one from Gregale's reserved public range when
// omitted. Listeners start disabled and require an explicit enable request.
type CreateUDPListenerRequest struct {
	Name       string `json:"name"`
	GuestPort  int    `json:"guest_port"`
	PublicPort int    `json:"public_port,omitempty"`
}

// UpdateUDPListenerRequest changes the serving state without changing the
// listener identity or its stable public endpoint.
type UpdateUDPListenerRequest struct {
	Enabled *bool `json:"enabled"`
}
