package api

import "time"

type RealtimeTicketRequest struct {
	Principal  string   `json:"principal"`
	Channels   []string `json:"channels"`
	TTLSeconds int      `json:"ttl_seconds"`
}
type RealtimeTicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}
