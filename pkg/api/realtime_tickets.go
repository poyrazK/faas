package api

import "time"

type RealtimeTicketRequest struct {
	Principal  string   `json:"principal"`
	Channels   []string `json:"channels"`
	TTLSeconds int      `json:"ttl_seconds"`
}
type RealtimeTicketResponse struct {
	Ticket string `json:"ticket"`
	// TicketID revokes this ticket without keeping the bearer token.
	TicketID  string    `json:"ticket_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RealtimeTicketsRevokeResponse reports how many live tickets a principal-wide
// revocation ended.
type RealtimeTicketsRevokeResponse struct {
	Revoked int `json:"revoked"`
}
