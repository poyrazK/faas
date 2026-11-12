// Package support owns Gregale's native customer support records and workflows.
package support

import (
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type Actor struct {
	ID       string
	Session  bool
	KeyOrgID string
}
type Scope struct {
	Key, Plan        string
	AccountID, OrgID string
}
type Calendar struct {
	Timezone    string   `json:"timezone"`
	Weekdays    []int    `json:"weekdays,omitempty"` // Sunday=0. Empty means continuous service.
	StartMinute int      `json:"start_minute"`
	EndMinute   int      `json:"end_minute"`
	Holidays    []string `json:"holidays,omitempty"`
}
type Policy struct {
	ID                   string   `json:"id"`
	Revision             int64    `json:"revision"`
	Name                 string   `json:"name"`
	Enabled              bool     `json:"enabled"`
	Order                int      `json:"order"`
	Plan                 string   `json:"plan,omitempty"`
	Category             string   `json:"category,omitempty"`
	Priority             string   `json:"priority,omitempty"`
	TeamID               string   `json:"team_id,omitempty"`
	FirstResponseSeconds int64    `json:"first_response_seconds"`
	NextReplySeconds     int64    `json:"next_reply_seconds"`
	ResolutionSeconds    int64    `json:"resolution_seconds"`
	ReopenWindowSeconds  int64    `json:"reopen_window_seconds"`
	CustomerReopensMax   int      `json:"customer_reopens_max"`
	Calendar             Calendar `json:"calendar"`
}
type Profile struct {
	Available bool       `json:"available"`
	Capacity  int        `json:"capacity"`
	Skills    []string   `json:"skills"`
	AwayUntil *time.Time `json:"away_until,omitempty"`
	Calendar  Calendar   `json:"calendar"`
}
type Staff struct {
	AccountID string  `json:"account_id"`
	Revision  int64   `json:"revision"`
	Role      string  `json:"role"`
	Profile   Profile `json:"profile"`
}
type Team struct {
	ID         string   `json:"id"`
	Revision   int64    `json:"revision"`
	Name       string   `json:"name"`
	Enabled    bool     `json:"enabled"`
	Members    []string `json:"members"`
	FallbackID string   `json:"fallback_id,omitempty"`
}
type Episode struct {
	ID                 string     `json:"id"`
	StartedAt          time.Time  `json:"started_at"`
	Plan               string     `json:"plan"`
	Priority           string     `json:"priority"`
	Category           string     `json:"category"`
	Policy             Policy     `json:"policy"`
	FirstResponseDue   time.Time  `json:"first_response_due"`
	FirstResponseAt    *time.Time `json:"first_response_at,omitempty"`
	NextReplyDue       *time.Time `json:"next_reply_due,omitempty"`
	ResolutionDue      time.Time  `json:"resolution_due"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
	WaitSince          *time.Time `json:"wait_since,omitempty"`
	RemainingUS        int64      `json:"remaining_us"`
	PausedUS           int64      `json:"paused_us"`
	ResolutionBreached bool       `json:"resolution_breached"`
	NextReplyCompleted int        `json:"next_reply_completed"`
	NextReplyMet       int        `json:"next_reply_met"`
}
type ticketState struct {
	Context         api.SupportContext `json:"context"`
	Episode         Episode            `json:"episode"`
	CustomerReopens int                `json:"customer_reopens"`
	RequiredSkills  []string           `json:"required_skills"`
}
type StaffTicket struct {
	Ticket          api.SupportTicket `json:"ticket"`
	Priority        string            `json:"priority"`
	TeamID          string            `json:"team_id,omitempty"`
	AssigneeID      string            `json:"assignee_id,omitempty"`
	Episode         Episode           `json:"episode"`
	RequiredSkills  []string          `json:"required_skills"`
	CustomerReopens int               `json:"customer_reopens"`
}
type StaffPage struct {
	Items      []StaffTicket `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}
type Change struct {
	ExpectedRevision int64    `json:"expected_revision"`
	Body             string   `json:"body,omitempty"`
	Status           string   `json:"status,omitempty"`
	Priority         string   `json:"priority,omitempty"`
	TeamID           string   `json:"team_id,omitempty"`
	AssigneeID       string   `json:"assignee_id,omitempty"`
	RequiredSkills   []string `json:"required_skills,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	Route            bool     `json:"route,omitempty"`
	OverrideCapacity bool     `json:"override_capacity,omitempty"`
}
type Engineering struct {
	Revision         int64  `json:"revision"`
	ExpectedRevision int64  `json:"expected_revision,omitempty"`
	OwnerID          string `json:"owner_id"`
	Severity         string `json:"severity"`
	Status           string `json:"status"`
	Reproduction     string `json:"reproduction"`
	IssueURL         string `json:"issue_url,omitempty"`
	Redacted         bool   `json:"redacted"`
}
type PreviewRequest struct {
	ExpectedRevision    int64  `json:"expected_revision"`
	MessageID           string `json:"message_id,omitempty"`
	Engineering         bool   `json:"engineering,omitempty"`
	Reason              string `json:"reason,omitempty"`
	Body                string `json:"body,omitempty"`
	EngineeringRevision int64  `json:"engineering_revision,omitempty"`
}
type Preview struct {
	ID                  string    `json:"id"`
	Revision            int64     `json:"revision"`
	ExpiresAt           time.Time `json:"expires_at"`
	MessageIDs          []string  `json:"message_ids,omitempty"`
	Engineering         bool      `json:"engineering,omitempty"`
	Reason              string    `json:"reason,omitempty"`
	Body                string    `json:"body,omitempty"`
	EngineeringRevision int64     `json:"engineering_revision,omitempty"`
	Hash                string    `json:"hash"`
}
type Approval struct {
	PreviewID        string `json:"preview_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type Event struct {
	ID        string    `json:"id"`
	Sequence  int64     `json:"sequence"`
	ActorID   string    `json:"actor_id,omitempty"`
	Kind      string    `json:"kind"`
	Data      any       `json:"data"`
	CreatedAt time.Time `json:"created_at"`
}

type Availability struct {
	ExpectedRevision int64      `json:"expected_revision"`
	Available        bool       `json:"available"`
	AwayUntil        *time.Time `json:"away_until,omitempty"`
}
