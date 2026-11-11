package api

import "time"

// SupportContext contains identifiers only; support never collects credentials,
// environment values, source code or workload logs automatically.
type SupportContext struct {
	AppID        string `json:"app_id,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
}
type CreateSupportTicketRequest struct {
	Priority string         `json:"priority,omitempty"`
	Subject  string         `json:"subject"`
	Category string         `json:"category"`
	Body     string         `json:"body"`
	Context  SupportContext `json:"context,omitempty"`
}
type SupportMessageRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Body             string `json:"body"`
}
type CloseSupportTicketRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}
type ReopenSupportTicketRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Body             string `json:"body"`
}
type SupportTicket struct {
	ID          string         `json:"id"`
	Subject     string         `json:"subject"`
	Category    string         `json:"category"`
	Status      string         `json:"status"`
	Revision    int64          `json:"revision"`
	Context     SupportContext `json:"context"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	ResolvedAt  *time.Time     `json:"resolved_at,omitempty"`
	CanReopen   bool           `json:"can_reopen"`
	ReopenUntil *time.Time     `json:"reopen_until,omitempty"`
}
type SupportTicketPage struct {
	Items      []SupportTicket `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}
type SupportMessage struct {
	ID        string    `json:"id"`
	Sequence  int64     `json:"sequence"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	Redacted  bool      `json:"redacted"`
	CreatedAt time.Time `json:"created_at"`
}
type SupportMessagePage struct {
	Items      []SupportMessage `json:"items"`
	NextCursor int64            `json:"next_cursor,omitempty"`
}
type SupportSubmission struct {
	TicketID     string `json:"ticket_id"`
	SubmissionID string `json:"submission_id"`
	Revision     int64  `json:"revision"`
}
type SupportSatisfactionRequest struct {
	EpisodeID string `json:"episode_id"`
	Rating    int32  `json:"rating"`
}
type SupportSatisfaction struct {
	EpisodeID string `json:"episode_id"`
	Eligible  bool   `json:"eligible"`
	Rating    *int32 `json:"rating,omitempty"`
}
