package api

// CreateNativeLinearIssueTicketRequest explicitly creates or recovers a Linear ticket.
type CreateNativeLinearIssueTicketRequest struct {
	RequestID   string `json:"request_id"`
	TeamID      string `json:"team_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Recover     bool   `json:"recover,omitempty"`
	RecoveryURL string `json:"recovery_url,omitempty"`
}
