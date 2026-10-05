package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrExecutionWorkflowJobExists = errors.New("state: managed execution workflow already exists")
	ErrExecutionWorkflowLeaseLost = errors.New("state: managed execution workflow lease lost")
	ErrExecutionWorkflowQueueFull = errors.New("state: managed execution workflow queue is full")
)

// ExecutionWorkflowManagedMaxActivePerAccount bounds encrypted plans retained
// for detached continuation. Terminal rows no longer retain plan ciphertext.
const ExecutionWorkflowManagedMaxActivePerAccount = 16

// ExecutionWorkflowJob is encrypted, bounded orchestration metadata. Plan
// plaintext is opened only by the apid continuation worker.
type ExecutionWorkflowJob struct {
	ID              string
	AccountID       string
	RunsPrincipalID *string
	WorkflowID      string
	PlanID          string
	Status          api.ManagedExecutionWorkflowStatus
	StepCount       int
	NextStep        int
	SealedPlan      []byte
	PayloadKID      string
	LeaseToken      *string
	LeaseOwner      *string
	LeaseExpiresAt  *time.Time
	ScheduledFor    time.Time
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	FinishedAt      *time.Time
}

type CreateExecutionWorkflowJobParams struct {
	AccountID       string
	RunsPrincipalID *string
	WorkflowID      string
	PlanID          string
	StepCount       int
	SealedPlan      []byte
	PayloadKID      string
	CreatedAt       time.Time
}

type ExecutionWorkflowJobUpdate struct {
	Status       api.ManagedExecutionWorkflowStatus
	NextStep     int
	ScheduledFor time.Time
	LastError    string
	UpdatedAt    time.Time
}

// ExecutionWorkflowJobStore is the durable queue seam used by apid's
// server-managed Runs workflow continuation worker.
type ExecutionWorkflowJobStore interface {
	CreateExecutionWorkflowJob(ctx context.Context, params CreateExecutionWorkflowJobParams) (ExecutionWorkflowJob, error)
	ClaimExecutionWorkflowJob(ctx context.Context, owner string, at time.Time, leaseDuration time.Duration) (ExecutionWorkflowJobClaim, error)
	UpdateExecutionWorkflowJob(ctx context.Context, id, leaseToken string, update ExecutionWorkflowJobUpdate) error
	ExecutionWorkflowJobByKey(ctx context.Context, accountID, workflowID string, principalID *string) (ExecutionWorkflowJob, error)
	ListExecutionWorkflowJobsByKey(ctx context.Context, accountID, workflowID string, principalID *string) ([]ExecutionWorkflowJob, error)
}

type ExecutionWorkflowJobClaim struct {
	ExecutionWorkflowJob
	ClaimToken string
}

func validateExecutionWorkflowJobParams(params CreateExecutionWorkflowJobParams) error {
	if params.AccountID == "" || params.WorkflowID == "" || api.ValidateExecutionWorkflowMetadata(params.WorkflowID, "") != nil {
		return fmt.Errorf("%w: invalid owner or workflow id", ErrExecutionInvalid)
	}
	if len(params.PlanID) != 24 || strings.Trim(params.PlanID, "0123456789abcdef") != "" {
		return fmt.Errorf("%w: invalid plan id", ErrExecutionInvalid)
	}
	if params.StepCount < 1 || params.StepCount > api.ExecutionWorkflowManagedMaxSteps ||
		len(params.SealedPlan) == 0 || len(params.SealedPlan) > api.ExecutionWorkflowManagedPlanMaxBytes+64*1024 ||
		strings.TrimSpace(params.PayloadKID) == "" || len(params.PayloadKID) > 255 || params.CreatedAt.IsZero() {
		return fmt.Errorf("%w: invalid encrypted workflow plan metadata", ErrExecutionInvalid)
	}
	return nil
}

func cloneExecutionWorkflowJob(row ExecutionWorkflowJob) ExecutionWorkflowJob {
	row.SealedPlan = append([]byte(nil), row.SealedPlan...)
	if row.RunsPrincipalID != nil {
		value := *row.RunsPrincipalID
		row.RunsPrincipalID = &value
	}
	if row.LeaseToken != nil {
		value := *row.LeaseToken
		row.LeaseToken = &value
	}
	if row.LeaseOwner != nil {
		value := *row.LeaseOwner
		row.LeaseOwner = &value
	}
	if row.LeaseExpiresAt != nil {
		value := *row.LeaseExpiresAt
		row.LeaseExpiresAt = &value
	}
	if row.FinishedAt != nil {
		value := *row.FinishedAt
		row.FinishedAt = &value
	}
	return row
}
