package referrals

import (
	"context"
	"errors"
	"time"
)

var ErrDuplicateLead = errors.New("sales lead already exists for this owner")

var ErrConflict = errors.New("sales record changed or already exists")

type Rep struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}
type ProspectInput struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Company string `json:"company"`
	Stage   string `json:"stage"`
	Note    string `json:"note"`
	OwnerID string `json:"owner_id"`
	Version int64  `json:"version"`
}
type Prospect struct {
	ProspectInput
	ID         string    `json:"id"`
	OwnerEmail string    `json:"owner_email"`
	Registered bool      `json:"registered"`
	Paid       bool      `json:"paid"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type PipelineFilter struct {
	Owner, Query, Stage, After string
	Limit                      int32
}
type StageCount struct {
	Stage string `json:"stage"`
	Count int64  `json:"count"`
}
type TaskInput struct {
	LeadID  string `json:"lead_id,omitempty"`
	Title   string `json:"title"`
	DueOn   string `json:"due_on"`
	Done    bool   `json:"done"`
	Version int64  `json:"version"`
}
type Task struct {
	TaskInput
	ID         string `json:"id"`
	LeadName   string `json:"lead_name"`
	LeadEmail  string `json:"lead_email"`
	OwnerID    string `json:"owner_id"`
	OwnerEmail string `json:"owner_email"`
}
type TaskFilter struct {
	Owner, Status, Due, On, AfterDate, AfterID string
	Limit                                      int32
}
type TaskCounts struct {
	Open    int64 `json:"open"`
	Today   int64 `json:"today"`
	Overdue int64 `json:"overdue"`
}

// PipelineStore is optional and separate from acquisition attribution. Assigning
// a prospect never grants account access or changes historical referral credit.
type PipelineStore interface {
	SalesTeam(context.Context, []string) ([]Rep, error)
	Prospects(context.Context, Scope, PipelineFilter) ([]Prospect, []StageCount, error)
	SaveProspect(context.Context, Scope, string, string, ProspectInput) (string, error)
	DeleteProspect(context.Context, Scope, string, int64) (bool, error)
	Tasks(context.Context, Scope, TaskFilter) ([]Task, TaskCounts, error)
	SaveTask(context.Context, Scope, string, string, TaskInput) (string, error)
}

func ProspectStages() []string {
	return []string{"new", "contacted", "qualified", "demo", "won", "lost"}
}
