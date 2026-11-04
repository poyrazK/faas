package referrals

import (
	"context"
	"time"
)

type ActivityInput struct {
	ID            string    `json:"id"`
	LeadVersion   int64     `json:"lead_version,omitempty"`
	Kind          string    `json:"kind"`
	Summary       string    `json:"summary"`
	Outcome       string    `json:"outcome"`
	NextStep      string    `json:"next_step"`
	OccurredAt    time.Time `json:"occurred_at"`
	FollowUpTitle string    `json:"follow_up_title"`
	FollowUpOn    string    `json:"follow_up_on"`
}
type Activity struct {
	ActivityInput
	ActorID    string    `json:"actor_id"`
	ActorEmail string    `json:"actor_email"`
	RecordedAt time.Time `json:"recorded_at"`
	TaskID     string    `json:"task_id"`
}
type ActivityFilter struct {
	AfterAt time.Time
	AfterID string
	Limit   int32
}
type AlertFilter struct {
	Owner, Kind, On, After string
	AsOf                   time.Time
	StaleDays              int32
	Limit                  int32
}
type LeadAlert struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	Company       string    `json:"company"`
	Stage         string    `json:"stage"`
	OwnerID       string    `json:"owner_id"`
	OwnerEmail    string    `json:"owner_email"`
	LastContactAt time.Time `json:"last_contact_at"`
	HasContact    bool      `json:"has_contact"`
	Stale         bool      `json:"stale"`
	OverdueTasks  int64     `json:"overdue_tasks"`
	EarliestDueOn string    `json:"earliest_due_on"`
}
type AlertCounts struct {
	Leads        int64 `json:"leads"`
	Stale        int64 `json:"stale"`
	OverdueLeads int64 `json:"overdue_leads"`
	OverdueTasks int64 `json:"overdue_tasks"`
}

// ActivityStore keeps staff history separate from acquisition/referral credit.
type ActivityStore interface {
	LeadActivity(context.Context, Scope, string, ActivityFilter) (Prospect, []Activity, error)
	RecordActivity(context.Context, Scope, string, ActivityInput) (string, error)
	LeadAlerts(context.Context, Scope, AlertFilter) ([]LeadAlert, AlertCounts, error)
}
