package api

// CreateNativeIssueTicketRequest explicitly creates or recovers a GitHub ticket.
type CreateNativeIssueTicketRequest struct {
	RequestID   string `json:"request_id"`
	Repository  string `json:"repository"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	RecoveryURL string `json:"recovery_url,omitempty"`
}

// NativeIssueTicketResult is durable creation evidence; created means linking needs retry.
type NativeIssueTicketResult struct {
	RequestID string `json:"request_id"`
	State     string `json:"state"`
	TicketURL string `json:"ticket_url"`
	LinkID    string `json:"link_id,omitempty"`
}
