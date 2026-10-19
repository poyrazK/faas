package ci

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("CI resource not found")
	ErrConflict = errors.New("CI request identity conflict")
	ErrLimit    = errors.New("internal CI metadata limit reached")
)

type LimitError struct {
	Resource string
	Limit    int
	Observed int
}

func (e *LimitError) Error() string { return "internal CI " + e.Resource + " metadata limit reached" }
func (e *LimitError) Unwrap() error { return ErrLimit }

const (
	StatusBlocked      = "blocked"
	StatusCancelled    = "cancelled"
	ComputeUnavailable = "compute_unavailable"
)

type Revision struct {
	ID         string     `json:"id"`
	AccountID  string     `json:"account_id"`
	Name       string     `json:"name"`
	Digest     string     `json:"digest"`
	Definition Definition `json:"definition"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Run struct {
	ID            string        `json:"id"`
	AccountID     string        `json:"account_id"`
	Pipeline      string        `json:"pipeline"`
	RevisionID    string        `json:"revision_id"`
	CommitSHA     string        `json:"commit_sha"`
	Status        string        `json:"status"`
	BlockedReason string        `json:"blocked_reason,omitempty"`
	Plan          Plan          `json:"plan"`
	CreatedAt     time.Time     `json:"created_at"`
	FinishedAt    *time.Time    `json:"finished_at,omitempty"`
	GitHub        *GitHubSource `json:"github,omitempty"`
}

type Event struct {
	Sequence int       `json:"sequence"`
	Type     string    `json:"type"`
	At       time.Time `json:"at"`
}

type SubmitRequest struct {
	RevisionID string `json:"revision_id"`
	CommitSHA  string `json:"commit_sha"`
}

// Store accepts only metadata. No method can enqueue a Job, issue a compute
// notification, execute a command, or transition a run to running/succeeded.
type Store interface {
	PutRevision(context.Context, string, string, Definition) (Revision, error)
	ListRevisions(context.Context, string, string) ([]Revision, error)
	Revision(context.Context, string, string) (Revision, error)
	Submit(context.Context, string, string, SubmitRequest) (Run, bool, error)
	ListRuns(context.Context, string) ([]Run, error)
	Run(context.Context, string, string) (Run, error)
	Cancel(context.Context, string, string) (Run, error)
	Events(context.Context, string, string) ([]Event, error)
}
