package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Execution is the durable, payload-free projection of one disposable
// one-shot execution. Source and input deliberately live in ExecutionClaim
// only, after a scheduler has acquired the row's lease.
type Execution struct {
	ID              string
	AccountID       string
	Runtime         api.ExecutionRuntime
	Status          api.ExecutionStatus
	NetworkMode     api.ExecutionNetworkMode
	Limits          api.ResolvedExecutionLimits
	SourceBytes     int
	InputBytes      int
	DeadlineAt      time.Time
	LeaseToken      *string
	LeaseOwner      *string
	LeaseExpiresAt  *time.Time
	CancelRequested *time.Time
	Result          json.RawMessage
	Stdout          string
	Stderr          string
	OutputTruncated bool
	ExitCode        *int
	FailureCode     *string
	FailureMessage  *string
	Usage           api.ExecutionUsage
	StartedAt       *time.Time
	FinishedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ExecutionClaim is returned only to the schedd claim path. The encrypted
// payload stays opaque to pkg/state and is never returned by customer reads.
type ExecutionClaim struct {
	Execution
	SealedPayload []byte
	PayloadKID    string
}

// CreateExecutionParams contains an already-resolved execution request and
// its caller-payload ciphertext. AdmittedAt and DeadlineAt are supplied
// together so queue time is part of the immutable wall-clock budget.
type CreateExecutionParams struct {
	AccountID     string
	Request       api.ResolvedExecutionRequest
	SourceBytes   int
	InputBytes    int
	AdmittedAt    time.Time
	DeadlineAt    time.Time
	SealedPayload []byte
	PayloadKID    string
}

// CompleteExecutionParams is the scheduler-owned compare-and-swap that makes
// an execution terminal. LeaseToken prevents a stale scheduler from
// overwriting a row recovered by a newer owner.
type CompleteExecutionParams struct {
	ID              string
	LeaseToken      string
	Status          api.ExecutionStatus
	Result          json.RawMessage
	Stdout          string
	Stderr          string
	OutputTruncated bool
	ExitCode        *int
	FailureCode     *string
	FailureMessage  *string
	Usage           api.ExecutionUsage
	FinishedAt      time.Time
}

// ExecutionSweepResult reports the recovery work completed in one bounded
// sweep. Restoring leases can be requeued because MarkExecutionRunning is the
// dispatch fence; running leases are terminalized and never replayed.
type ExecutionSweepResult struct {
	ExpiredQueued    int
	RequeuedRestores int
	FinishedRestores int
	FinishedRuns     int
	PayloadsDeleted  int
}

// ExecutionQuotaError is returned when atomic admission observes the active
// per-account limit. Observed includes the execution the caller attempted to
// admit, matching the API problem-detail convention.
type ExecutionQuotaError struct {
	Limit    int
	Observed int
}

func (e *ExecutionQuotaError) Error() string {
	return fmt.Sprintf("state: execution concurrency exceeded (limit=%d, observed=%d)", e.Limit, e.Observed)
}

func (e *ExecutionQuotaError) Is(target error) bool {
	return target == ErrExecutionQuotaExceeded
}

var (
	ErrExecutionQuotaExceeded   = errors.New("state: execution concurrency exceeded")
	ErrExecutionsNotAllowed     = errors.New("state: executions are not allowed for account plan")
	ErrExecutionLeaseLost       = errors.New("state: execution lease lost")
	ErrExecutionInvalid         = errors.New("state: invalid execution")
	ErrExecutionInvalidTerminal = errors.New("state: invalid execution terminal transition")
)

// ExecutionStore is split from Store so scheduler-focused tests and future
// daemons can depend on the narrow ownership surface.
type ExecutionStore interface {
	CreateExecution(ctx context.Context, params CreateExecutionParams) (Execution, error)
	ExecutionByID(ctx context.Context, accountID, executionID string) (Execution, error)
	ListExecutions(ctx context.Context, accountID string, limit, offset int) ([]Execution, error)
	ClaimExecution(ctx context.Context, owner string, claimedAt time.Time, leaseDuration time.Duration) (ExecutionClaim, error)
	MarkExecutionRunning(ctx context.Context, executionID, leaseToken string, startedAt time.Time) (Execution, error)
	RenewExecutionLease(ctx context.Context, executionID, leaseToken string, renewedAt time.Time, leaseDuration time.Duration) error
	CompleteExecution(ctx context.Context, params CompleteExecutionParams) (Execution, error)
	RequestExecutionCancellation(ctx context.Context, accountID, executionID string, requestedAt time.Time) (Execution, error)
	SweepExecutions(ctx context.Context, at time.Time, limit int) (ExecutionSweepResult, error)
}

func validateCreateExecution(params CreateExecutionParams) error {
	if params.AccountID == "" {
		return fmt.Errorf("%w: account id is required", ErrExecutionInvalid)
	}
	if !params.Request.Runtime.Valid() || params.Request.Network.Mode != api.ExecutionNetworkNone {
		return fmt.Errorf("%w: unsupported runtime or network mode", ErrExecutionInvalid)
	}
	limits := params.Request.Limits
	if limits.TimeoutMS < api.ExecutionTimeoutMinMS || limits.TimeoutMS > api.ExecutionTimeoutHardMaxMS ||
		!api.ValidExecutionMemoryMB(limits.MemoryMB) ||
		!api.ValidAppCPUMillicores(limits.CPUMillicores) ||
		!api.ValidExecutionEphemeralDiskMB(limits.EphemeralDiskMB) ||
		limits.MaxOutputBytes < api.ExecutionOutputMinBytes ||
		limits.MaxOutputBytes > api.ExecutionOutputHardMaxBytes || limits.PIDsMax != api.ExecutionPIDsMax {
		return fmt.Errorf("%w: resolved resource envelope is outside hard bounds", ErrExecutionInvalid)
	}
	if params.SourceBytes < 1 || params.SourceBytes > api.ExecutionPlaintextFieldMaxBytes ||
		params.InputBytes < 0 || params.InputBytes > api.ExecutionPlaintextFieldMaxBytes {
		return fmt.Errorf("%w: plaintext byte counts are outside hard bounds", ErrExecutionInvalid)
	}
	if len(params.SealedPayload) == 0 || len(params.SealedPayload) > api.ExecutionSealedPayloadMaxBytes ||
		strings.TrimSpace(params.PayloadKID) == "" || len(params.PayloadKID) > 255 {
		return fmt.Errorf("%w: sealed payload or key id is outside hard bounds", ErrExecutionInvalid)
	}
	if params.AdmittedAt.IsZero() || !params.DeadlineAt.After(params.AdmittedAt) ||
		params.DeadlineAt.Sub(params.AdmittedAt) != time.Duration(limits.TimeoutMS)*time.Millisecond {
		return fmt.Errorf("%w: deadline must equal admitted_at plus timeout_ms", ErrExecutionInvalid)
	}
	return nil
}

func validateExecutionPlan(params CreateExecutionParams, limits api.ExecutionPlanLimits) error {
	request := params.Request.Limits
	if !limits.Allowed || limits.MaxConcurrent <= 0 {
		return ErrExecutionsNotAllowed
	}
	if params.SourceBytes > limits.MaxSourceBytes || params.InputBytes > limits.MaxInputBytes ||
		request.TimeoutMS > limits.MaxTimeoutMS || request.MemoryMB > limits.MaxMemoryMB ||
		request.CPUMillicores > limits.MaxCPUMillicores ||
		request.EphemeralDiskMB > limits.MaxEphemeralDiskMB || request.MaxOutputBytes > limits.MaxOutputBytes {
		return fmt.Errorf("%w: resolved envelope exceeds the account plan", ErrExecutionInvalid)
	}
	return nil
}

func validateCompletion(params CompleteExecutionParams, maxOutputBytes int) error {
	if params.ID == "" || params.LeaseToken == "" || params.FinishedAt.IsZero() || !params.Status.Terminal() {
		return ErrExecutionInvalidTerminal
	}
	if params.Status == api.ExecutionStatusSucceeded {
		if len(params.Result) != 0 && !json.Valid(params.Result) {
			return fmt.Errorf("%w: result is not valid JSON", ErrExecutionInvalidTerminal)
		}
	} else if len(params.Result) != 0 {
		return fmt.Errorf("%w: only succeeded executions may store a result", ErrExecutionInvalidTerminal)
	}
	if len(params.Result)+len(params.Stdout)+len(params.Stderr) > maxOutputBytes {
		return fmt.Errorf("%w: combined output exceeds admitted budget", ErrExecutionInvalidTerminal)
	}
	if params.ExitCode != nil && (*params.ExitCode < 0 || *params.ExitCode > 255) {
		return fmt.Errorf("%w: exit code is outside 0..255", ErrExecutionInvalidTerminal)
	}
	if params.FailureCode != nil && (len(*params.FailureCode) == 0 || len(*params.FailureCode) > 64) {
		return fmt.Errorf("%w: failure code is outside hard bounds", ErrExecutionInvalidTerminal)
	}
	if params.FailureCode != nil && params.Status != api.ExecutionStatusFailed &&
		params.Status != api.ExecutionStatusTimedOut && params.Status != api.ExecutionStatusOutOfMemory {
		return fmt.Errorf("%w: failure code is not valid for terminal status", ErrExecutionInvalidTerminal)
	}
	if (params.FailureCode == nil) != (params.FailureMessage == nil) {
		return fmt.Errorf("%w: failure code and message must be supplied together", ErrExecutionInvalidTerminal)
	}
	if params.FailureMessage != nil && len(*params.FailureMessage) > 4096 {
		return fmt.Errorf("%w: failure message is outside hard bounds", ErrExecutionInvalidTerminal)
	}
	if params.Usage.WallTimeMS < 0 || params.Usage.CPUTimeMS < 0 || params.Usage.PeakMemoryMB < 0 {
		return fmt.Errorf("%w: usage cannot be negative", ErrExecutionInvalidTerminal)
	}
	return nil
}

func normalizeExecutionPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
