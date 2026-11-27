package tasktoolrehearsal

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const smokeReportResult = "Report hosted-task-rehearsal is ready after 20 steps."

// SmokeScope identifies a single owner-approved rehearsal. It carries
// identities only. A host adapter resolves credentials inside containment.
type SmokeScope struct {
	AccountID, RunID, Namespace, PlanSHA256 string
	WebAppID, WorkerAppID, ObserverAppID    string
	WebDeploymentID, WorkerDeploymentID     string
}

type WorkerClaim struct {
	TaskID, Namespace, DeploymentID, InstanceID, Status string
	Attempt                                             int
}

type StoppedWorker struct {
	DeploymentID, InstanceID string
}

type SmokeResult struct {
	TaskID, Status, Text string
}

type ObservedResult struct {
	TaskID, Namespace                      string
	Attempt, TaskRows, CompletedResultRows int
}

// SmokeEvidence is private observation data for the operator receipt. The
// caller records cleanup and host acceptance independently of this result.
type SmokeEvidence struct {
	TaskID           string
	Initial          WorkerClaim
	Stopped          StoppedWorker
	Recovered        WorkerClaim
	Result           SmokeResult
	Observed         ObservedResult
	CleanupConfirmed bool
}

// SmokeDriver is implemented inside the dedicated acceptance harness. The
// scheduler owns worker stops. The observer reads PostgreSQL independently
// of the MCP result, using the run's read-only credential.
type SmokeDriver interface {
	CheckHost(ctx context.Context, accountID string) error
	// SubmitReport submits build_report for hosted-task-rehearsal with 20 steps
	// through the pinned web deployment.
	SubmitReport(ctx context.Context, scope SmokeScope) (taskID string, err error)
	WaitForClaim(ctx context.Context, scope SmokeScope, taskID string, attempt int) (WorkerClaim, error)
	// StopWorker returns only after scheduler-owned termination of the
	// exact instance has been confirmed against the scope.
	StopWorker(ctx context.Context, scope SmokeScope, claim WorkerClaim) (StoppedWorker, error)
	WaitForResult(ctx context.Context, scope SmokeScope, taskID string) (SmokeResult, error)
	ObserveResult(ctx context.Context, scope SmokeScope, taskID string) (ObservedResult, error)
	VerifyCleanup(ctx context.Context, scope SmokeScope) error
}

// RunSmoke qualifies a Task on the already prepared, contained runtime. The
// surrounding owner claim determines who may call it and bounds the lifetime.
func RunSmoke(ctx context.Context, scope SmokeScope, driver SmokeDriver) (SmokeEvidence, error) {
	var evidence SmokeEvidence
	if driver == nil {
		return evidence, errors.New("missing rehearsal smoke driver")
	}
	if err := ctx.Err(); err != nil {
		return evidence, err
	}
	taskID, err := driver.SubmitReport(ctx, scope)
	if err != nil {
		return evidence, fmt.Errorf("submit rehearsal report: %w", err)
	}
	if !validSmokeID(taskID) {
		return evidence, errors.New("invalid rehearsal Task identity")
	}
	evidence.TaskID = taskID
	initial, err := driver.WaitForClaim(ctx, scope, taskID, 1)
	if err != nil {
		return evidence, fmt.Errorf("observe original rehearsal claim: %w", err)
	}
	if !matchesClaim(scope, initial, taskID, 1) {
		return evidence, errors.New("original worker claim does not match the rehearsal")
	}
	evidence.Initial = initial
	if err := ctx.Err(); err != nil {
		return evidence, err
	}
	stopped, err := driver.StopWorker(ctx, scope, initial)
	if err != nil {
		return evidence, fmt.Errorf("interrupt original rehearsal worker: %w", err)
	}
	if stopped.InstanceID != initial.InstanceID || stopped.DeploymentID != initial.DeploymentID {
		return evidence, errors.New("worker stop observation does not match the original claim")
	}
	evidence.Stopped = stopped

	recovered, err := driver.WaitForClaim(ctx, scope, taskID, 2)
	if err != nil {
		return evidence, fmt.Errorf("observe replacement rehearsal claim: %w", err)
	}
	if !matchesClaim(scope, recovered, taskID, 2) || recovered.InstanceID == initial.InstanceID {
		return evidence, errors.New("replacement worker claim does not match the rehearsal")
	}
	evidence.Recovered = recovered
	result, err := driver.WaitForResult(ctx, scope, taskID)
	if err != nil {
		return evidence, fmt.Errorf("read rehearsal MCP result: %w", err)
	}
	if result != (SmokeResult{TaskID: taskID, Status: "completed", Text: smokeReportResult}) {
		return evidence, errors.New("MCP result does not match the rehearsal report")
	}
	evidence.Result = result
	observed, err := driver.ObserveResult(ctx, scope, taskID)
	if err != nil {
		return evidence, fmt.Errorf("read rehearsal PostgreSQL observation: %w", err)
	}
	if observed != (ObservedResult{TaskID: taskID, Namespace: scope.Namespace, Attempt: 2, TaskRows: 1, CompletedResultRows: 1}) {
		return evidence, errors.New("expected exactly one durable result for the recovered rehearsal Task")
	}
	evidence.Observed = observed
	return evidence, nil
}

func matchesClaim(scope SmokeScope, claim WorkerClaim, taskID string, attempt int) bool {
	return claim.TaskID == taskID && claim.Namespace == scope.Namespace && claim.Status == "running" &&
		claim.DeploymentID == scope.WorkerDeploymentID && claim.Attempt == attempt && validSmokeID(claim.InstanceID)
}

func validSmokeID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}
