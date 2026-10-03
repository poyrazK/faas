package state

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var ErrEnvironmentGitManaged = errors.New("setting is managed by the environment Git source")

// EnvironmentGitSourceSpec is populated only after server-side GitHub
// installation/repository identity verification. A ref selects candidates;
// reconciliation always executes a persisted exact commit SHA.
type EnvironmentGitSourceSpec = api.EnvironmentGitSourceSpec

type EnvironmentGitSource = api.EnvironmentGitSource

type EnvironmentDesiredRevision = api.EnvironmentDesiredRevision

type EnvironmentGitRevisionApproval = api.EnvironmentGitRevisionApproval

type EnvironmentGitApprovalStore interface {
	EnvironmentGitRevisionApproval(context.Context, string, string, string) (EnvironmentGitRevisionApproval, error)
}

type ApproveEnvironmentRevision struct {
	AccountID          string
	SourceID           string
	ExpectedGeneration int64
	CommitSHA          string
	Desired            environmentsync.DesiredState
	ApprovedBy         string
}

type EnvironmentGitOpsLease struct {
	RunID        string                     `json:"run_id"`
	Source       EnvironmentGitSource       `json:"source"`
	Revision     EnvironmentDesiredRevision `json:"revision"`
	LeaseToken   string                     `json:"-"`
	LeaseUntil   time.Time                  `json:"lease_until"`
	AttemptCount int                        `json:"attempt_count"`
}

type EnvironmentGitOpsRun = api.EnvironmentGitOpsRun

type EnvironmentGitOpsObservation struct {
	State     environmentsync.ObservedState
	Owners    []environmentsync.Ownership
	Overrides []environmentsync.Override
}

type EnvironmentGitOpsStep struct {
	Resource string `json:"resource"`
	Path     string `json:"path"`
	Action   string `json:"action"`
	Status   string `json:"status"`
}

// Intent operations use the same transaction for lease fencing, a fresh
// observation, plan verification, ownership changes, and customer intent.
type EnvironmentGitOpsIntentStore interface {
	ObserveEnvironmentGitOps(context.Context, EnvironmentGitOpsLease, environmentsync.DesiredState) (EnvironmentGitOpsObservation, error)
	ApplyEnvironmentGitOps(context.Context, EnvironmentGitOpsLease, environmentsync.Plan) ([]EnvironmentGitOpsStep, error)
	PreviewEnvironmentGitOpsAdoption(context.Context, string, string) (environmentsync.Plan, error)
	AdoptEnvironmentGitOps(context.Context, string, string, string) error
}

type EnvironmentGitSourceUpdate = api.EnvironmentGitSourceUpdate

type EnvironmentGitOpsOverrideRequest = api.EnvironmentGitOpsOverrideRequest

type EnvironmentGitOpsControlStore interface {
	UpdateEnvironmentGitSource(context.Context, string, string, EnvironmentGitSourceUpdate) (EnvironmentGitSource, error)
	SetEnvironmentGitOpsOverride(context.Context, string, string, EnvironmentGitOpsOverrideRequest) error
	RemoveEnvironmentGitOpsOverride(context.Context, string, string, string, string) error
}

// Optional narrow interface keeps the control-plane worker independent of
// the runtime Store facade. Mutation/adoption methods will extend this surface.
type EnvironmentGitOpsStore interface {
	CreateEnvironmentGitSource(context.Context, string, string, string, EnvironmentGitSourceSpec) (EnvironmentGitSource, error)
	EnvironmentGitSource(context.Context, string, string, string) (EnvironmentGitSource, error)
	ApproveEnvironmentDesiredRevision(context.Context, ApproveEnvironmentRevision) (EnvironmentGitSource, EnvironmentDesiredRevision, error)
	ClaimEnvironmentGitOps(context.Context, string, time.Time, time.Duration) (EnvironmentGitOpsLease, error)
	RenewEnvironmentGitOps(context.Context, EnvironmentGitOpsLease, time.Time, time.Duration) error
	FinishEnvironmentGitOps(context.Context, EnvironmentGitOpsLease, string, json.RawMessage, json.RawMessage, string, time.Time, time.Time) error
	ListEnvironmentGitOpsRuns(context.Context, string, string, int) ([]EnvironmentGitOpsRun, error)
}

// Mode claims select eligible sources inside the same transaction that issues
// the lease. A report controller must never claim an enforce source and leave
// it waiting for expiry, or trust a mode read before the claim.
type EnvironmentGitOpsModeClaimStore interface {
	ClaimEnvironmentGitOpsMode(context.Context, string, string, time.Time, time.Duration) (EnvironmentGitOpsLease, error)
}

var environmentCommitRE = regexp.MustCompile(`^([a-f0-9]{40}|[a-f0-9]{64})$`)
