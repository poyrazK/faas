package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	defaultExecutionLeaseDuration      = 15 * time.Second
	defaultExecutionLeaseRenewInterval = 5 * time.Second
	defaultExecutionPollInterval       = 200 * time.Millisecond
	defaultExecutionSweepInterval      = time.Second
	defaultExecutionDestroyTimeout     = 10 * time.Second
	defaultExecutionFinalizeTimeout    = 5 * time.Second
	defaultExecutionSweepLimit         = 100
)

// ErrExecutionCoordinatorNotWired prevents an enabled coordinator from
// claiming durable work without both its store and disposable-VM backend.
var ErrExecutionCoordinatorNotWired = errors.New("sched: execution coordinator is not fully wired")

// ExecutionCoordinatorConfig controls scheduler mechanics, not caller-visible
// resource limits. The latter remain centralized in pkg/api/limits.go.
// Enabled defaults to false so adding the coordinator to schedd cannot expose
// execution before the vmmd adapter and isolation acceptance suite land.
type ExecutionCoordinatorConfig struct {
	Enabled            bool
	Owner              string
	MaxConcurrent      int
	LeaseDuration      time.Duration
	LeaseRenewInterval time.Duration
	PollInterval       time.Duration
	SweepInterval      time.Duration
	DestroyTimeout     time.Duration
	FinalizeTimeout    time.Duration
	SweepLimit         int
}

// ExecutionRestoreRequest is the payload-free machine envelope supplied to
// the disposable-VM backend. Restore must prepare a fresh jail, cgroup, and
// scratch drive with no tenant network namespace, but must not run caller code.
type ExecutionRestoreRequest struct {
	ID          string
	AccountID   string
	NodeID      string
	Plan        api.Plan
	Runtime     api.ExecutionRuntime
	NetworkMode api.ExecutionNetworkMode
	Limits      api.ResolvedExecutionLimits
	DeadlineAt  time.Time
	// Machine fields are resolved by the scheduler from the immutable runtime
	// snapshot catalog. They are payload-free and are forwarded only to vmmd's
	// dedicated RestoreExecution RPC.
	KernelKey     string
	BaseKey       string
	LayerKey      string
	Snapshot      SnapshotRef
	VcpuCount     int
	MemSizeMiB    int
	CPUMillicores int
}

// ExecutionPayload stays opaque to the coordinator. The future vmmd adapter
// owns authenticated decryption and delivery to the guest after restore.
type ExecutionPayload struct {
	Sealed []byte
	KID    string
}

// ExecutionOutcome is the bounded, caller-safe result returned by a restored
// guest session. Host paths, protocol frames, and backend errors must not be
// placed in FailureMessage.
type ExecutionOutcome struct {
	Status          api.ExecutionStatus
	Result          json.RawMessage
	Stdout          string
	Stderr          string
	OutputTruncated bool
	ExitCode        *int
	FailureCode     string
	FailureMessage  string
	Usage           api.ExecutionUsage
}

// ExecutionSession is one restored-or-cold-booted disposable microVM.
// Execute must honor ctx cancellation. Destroy must be idempotent.
type ExecutionSession interface {
	Execute(ctx context.Context, payload ExecutionPayload) (ExecutionOutcome, error)
	Destroy(ctx context.Context) error
}

// ExecutionBackend prepares isolated sessions. If Restore returns an error it
// must already have removed every partial host allocation it created. Once a
// session is returned, the coordinator calls Destroy on every path.
type ExecutionBackend interface {
	Restore(ctx context.Context, request ExecutionRestoreRequest) (ExecutionSession, error)
}

// ExecutionCoordinator claims durable execution intents, maintains their
// leases, fences payload dispatch, and acknowledges terminal state only after
// the disposable VM has been destroyed.
type ExecutionCoordinator struct {
	store         state.ExecutionStore
	backend       ExecutionBackend
	claimResolver ExecutionClaimRequestResolver
	config        ExecutionCoordinatorConfig
	log           *slog.Logger
	now           func() time.Time
}

// ExecutionClaimRequestResolver supplies the trusted machine envelope for a
// claimed execution. Keeping this as an optional seam preserves the narrow
// coordinator tests while allowing schedd to attach catalog/plan resolution.
type ExecutionClaimRequestResolver interface {
	ResolveExecutionClaim(context.Context, state.ExecutionClaim) (ExecutionRestoreRequest, error)
}

// NewExecutionCoordinator constructs a disabled-by-default scheduler. Callers
// must explicitly set config.Enabled before Run will claim work.
func NewExecutionCoordinator(store state.ExecutionStore, backend ExecutionBackend, config ExecutionCoordinatorConfig, log *slog.Logger) *ExecutionCoordinator {
	if log == nil {
		log = slog.Default()
	}
	return &ExecutionCoordinator{
		store: store, backend: backend, config: normalizeExecutionCoordinatorConfig(config), log: log, now: time.Now,
	}
}

// WithClaimResolver attaches the scheduler-owned claim-to-machine resolver.
// A nil resolver leaves the legacy request projection in place; production
// execution dispatch supplies one before enabling the coordinator.
func (c *ExecutionCoordinator) WithClaimResolver(resolver ExecutionClaimRequestResolver) *ExecutionCoordinator {
	if c != nil {
		c.claimResolver = resolver
	}
	return c
}

func normalizeExecutionCoordinatorConfig(config ExecutionCoordinatorConfig) ExecutionCoordinatorConfig {
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		config.Owner = "schedd"
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 1
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = defaultExecutionLeaseDuration
	}
	if config.LeaseRenewInterval <= 0 || config.LeaseRenewInterval >= config.LeaseDuration {
		config.LeaseRenewInterval = config.LeaseDuration / 3
	}
	if config.LeaseRenewInterval <= 0 {
		config.LeaseRenewInterval = time.Millisecond
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultExecutionPollInterval
	}
	if config.SweepInterval <= 0 {
		config.SweepInterval = defaultExecutionSweepInterval
	}
	if config.DestroyTimeout <= 0 {
		config.DestroyTimeout = defaultExecutionDestroyTimeout
	}
	if config.FinalizeTimeout <= 0 {
		config.FinalizeTimeout = defaultExecutionFinalizeTimeout
	}
	if config.SweepLimit <= 0 {
		config.SweepLimit = defaultExecutionSweepLimit
	}
	if config.SweepLimit > 1000 {
		config.SweepLimit = 1000
	}
	return config
}

// Run performs one recovery sweep, then starts a bounded claim pool and a
// periodic recovery sweep. It returns nil after ctx cancellation.
func (c *ExecutionCoordinator) Run(ctx context.Context) error {
	if c == nil || !c.config.Enabled {
		return nil
	}
	if c.store == nil || c.backend == nil {
		return ErrExecutionCoordinatorNotWired
	}
	if _, err := c.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		c.log.Warn("schedd: execution initial recovery sweep failed", "err", err)
	}

	var workers sync.WaitGroup
	workers.Add(c.config.MaxConcurrent + 1)
	for i := 0; i < c.config.MaxConcurrent; i++ {
		go func() {
			defer workers.Done()
			c.runWorker(ctx)
		}()
	}
	go func() {
		defer workers.Done()
		c.runSweeper(ctx)
	}()
	<-ctx.Done()
	workers.Wait()
	return nil
}

// ProcessNext claims and fully handles at most one execution. The bool is
// false when no eligible queued intent exists.
func (c *ExecutionCoordinator) ProcessNext(ctx context.Context) (bool, error) {
	if c == nil || c.store == nil || c.backend == nil {
		return false, ErrExecutionCoordinatorNotWired
	}
	claim, err := c.store.ClaimExecution(ctx, c.config.Owner, c.now().UTC(), c.config.LeaseDuration)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sched: claim execution: %w", err)
	}
	if claim.LeaseToken == nil || *claim.LeaseToken == "" {
		return true, fmt.Errorf("sched: claimed execution %s without lease token", claim.ID)
	}
	return true, c.processClaim(ctx, claim)
}

// SweepOnce recovers expired durable intents. Restores may requeue; running
// rows are terminalized by the store and are never replayed.
func (c *ExecutionCoordinator) SweepOnce(ctx context.Context) (state.ExecutionSweepResult, error) {
	if c == nil || c.store == nil {
		return state.ExecutionSweepResult{}, ErrExecutionCoordinatorNotWired
	}
	result, err := c.store.SweepExecutions(ctx, c.now().UTC(), c.config.SweepLimit)
	if err != nil {
		return state.ExecutionSweepResult{}, fmt.Errorf("sched: sweep executions: %w", err)
	}
	return result, nil
}

type executionLeaseSignal uint8

const (
	executionLeaseHealthy executionLeaseSignal = iota
	executionLeaseCancelled
	executionLeaseLost
)

func (c *ExecutionCoordinator) processClaim(parent context.Context, claim state.ExecutionClaim) error {
	defer clear(claim.SealedPayload)
	workCtx, cancelWork := context.WithDeadline(parent, claim.DeadlineAt)
	defer cancelWork()
	monitorCtx, stopMonitor := context.WithCancel(parent)
	signals := make(chan executionLeaseSignal, 1)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		c.monitorLease(monitorCtx, claim, cancelWork, signals)
	}()
	stopLeaseMonitor := func() executionLeaseSignal {
		stopMonitor()
		<-monitorDone
		return latestExecutionLeaseSignal(signals)
	}

	request := ExecutionRestoreRequest{
		ID: claim.ID, AccountID: claim.AccountID, Runtime: claim.Runtime,
		NetworkMode: claim.NetworkMode, Limits: claim.Limits, DeadlineAt: claim.DeadlineAt,
	}
	var resolveErr error
	if c.claimResolver != nil {
		request, resolveErr = c.claimResolver.ResolveExecutionClaim(workCtx, claim)
	}
	if resolveErr != nil {
		signal := stopLeaseMonitor()
		if handled, err := c.finishInterrupted(parent, workCtx, claim, signal); handled {
			return err
		}
		c.log.Warn("schedd: execution claim resolution failed", "execution_id", claim.ID, "err", resolveErr)
		return c.complete(parent, claim, executionFailure(
			"restore_failed", "execution environment could not be prepared",
		), c.now().UTC())
	}
	session, restoreErr := c.backend.Restore(workCtx, request)
	if restoreErr != nil || session == nil {
		signal := stopLeaseMonitor()
		if restoreErr == nil {
			restoreErr = errors.New("backend returned a nil execution session")
		}
		if handled, err := c.finishInterrupted(parent, workCtx, claim, signal); handled {
			return err
		}
		c.log.Warn("schedd: execution restore failed", "execution_id", claim.ID, "err", restoreErr)
		return c.complete(parent, claim, executionFailure(
			"restore_failed", "execution environment could not be prepared",
		), c.now().UTC())
	}

	markAt := c.now().UTC()
	_, markErr := c.store.MarkExecutionRunning(parent, claim.ID, *claim.LeaseToken, markAt)
	if markErr != nil {
		destroyErr := c.destroy(parent, claim.ID, session)
		signal := stopLeaseMonitor()
		if destroyErr != nil {
			return destroyErr
		}
		if handled, err := c.finishInterrupted(parent, workCtx, claim, signal); handled {
			return err
		}
		return fmt.Errorf("sched: mark execution %s running: %w", claim.ID, markErr)
	}

	payload := ExecutionPayload{
		Sealed: append([]byte(nil), claim.SealedPayload...), KID: claim.PayloadKID,
	}
	outcome, executeErr := session.Execute(workCtx, payload)
	clear(payload.Sealed)
	destroyErr := c.destroy(parent, claim.ID, session)
	signal := stopLeaseMonitor()
	if destroyErr != nil {
		return destroyErr
	}
	if handled, err := c.finishInterrupted(parent, workCtx, claim, signal); handled {
		return err
	}
	if executeErr != nil {
		c.log.Warn("schedd: execution transport failed", "execution_id", claim.ID, "err", executeErr)
		outcome = executionFailure("execution_transport_failed", "execution result channel closed unexpectedly")
	}
	outcome = normalizeExecutionOutcome(outcome, claim.Limits.MaxOutputBytes)
	return c.complete(parent, claim, outcome, c.now().UTC())
}

func (c *ExecutionCoordinator) monitorLease(ctx context.Context, claim state.ExecutionClaim, cancelWork context.CancelFunc, signals chan<- executionLeaseSignal) {
	ticker := time.NewTicker(c.config.LeaseRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewedAt := c.now().UTC()
			err := c.store.RenewExecutionLease(ctx, claim.ID, *claim.LeaseToken, renewedAt, c.config.LeaseDuration)
			if err == nil {
				continue
			}
			signal := executionLeaseLost
			if errors.Is(err, state.ErrExecutionLeaseLost) {
				row, readErr := c.store.ExecutionByID(ctx, claim.AccountID, claim.ID)
				if readErr == nil && row.CancelRequested != nil && row.LeaseToken != nil && *row.LeaseToken == *claim.LeaseToken {
					signal = executionLeaseCancelled
				}
			}
			select {
			case signals <- signal:
			default:
			}
			cancelWork()
			return
		}
	}
}

func latestExecutionLeaseSignal(signals <-chan executionLeaseSignal) executionLeaseSignal {
	select {
	case signal := <-signals:
		return signal
	default:
		return executionLeaseHealthy
	}
}

// finishInterrupted reports whether normal backend result handling must stop.
// Cancellation is acknowledged after teardown. Deadline expiry is delegated
// to the durable sweeper because leases are capped at DeadlineAt.
func (c *ExecutionCoordinator) finishInterrupted(parent, workCtx context.Context, claim state.ExecutionClaim, signal executionLeaseSignal) (bool, error) {
	if parent.Err() != nil {
		return true, parent.Err()
	}
	if signal == executionLeaseCancelled {
		return true, c.complete(parent, claim, ExecutionOutcome{Status: api.ExecutionStatusCancelled}, c.now().UTC())
	}
	if errors.Is(workCtx.Err(), context.DeadlineExceeded) || !c.now().Before(claim.DeadlineAt) {
		sweepCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), c.config.FinalizeTimeout)
		defer cancel()
		_, err := c.SweepOnce(sweepCtx)
		return true, err
	}
	if signal == executionLeaseLost {
		return true, state.ErrExecutionLeaseLost
	}
	return false, nil
}

func (c *ExecutionCoordinator) destroy(parent context.Context, executionID string, session ExecutionSession) error {
	destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), c.config.DestroyTimeout)
	defer cancel()
	if err := session.Destroy(destroyCtx); err != nil {
		c.log.Error("schedd: execution teardown failed; terminal acknowledgement withheld", "execution_id", executionID, "err", err)
		return fmt.Errorf("sched: destroy execution %s: %w", executionID, err)
	}
	return nil
}

func (c *ExecutionCoordinator) complete(parent context.Context, claim state.ExecutionClaim, outcome ExecutionOutcome, finishedAt time.Time) error {
	completeCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), c.config.FinalizeTimeout)
	defer cancel()
	params := completionParams(claim, outcome, finishedAt)
	_, err := c.store.CompleteExecution(completeCtx, params)
	if err == nil {
		return nil
	}
	// Cancellation can race a successful Execute after the last renewal.
	// Re-read the durable row and acknowledge it only with the same lease.
	if errors.Is(err, state.ErrExecutionInvalidTerminal) {
		row, readErr := c.store.ExecutionByID(completeCtx, claim.AccountID, claim.ID)
		if readErr == nil && row.CancelRequested != nil && row.LeaseToken != nil && *row.LeaseToken == *claim.LeaseToken {
			_, err = c.store.CompleteExecution(completeCtx, completionParams(
				claim, ExecutionOutcome{Status: api.ExecutionStatusCancelled}, c.now().UTC(),
			))
		}
	}
	if err != nil {
		return fmt.Errorf("sched: complete execution %s: %w", claim.ID, err)
	}
	return nil
}

func completionParams(claim state.ExecutionClaim, outcome ExecutionOutcome, finishedAt time.Time) state.CompleteExecutionParams {
	var failureCode, failureMessage *string
	if outcome.FailureCode != "" || outcome.FailureMessage != "" {
		failureCode = stringPointer(outcome.FailureCode)
		failureMessage = stringPointer(outcome.FailureMessage)
	}
	return state.CompleteExecutionParams{
		ID: claim.ID, LeaseToken: *claim.LeaseToken, Status: outcome.Status,
		Result: append(json.RawMessage(nil), outcome.Result...), Stdout: outcome.Stdout, Stderr: outcome.Stderr,
		OutputTruncated: outcome.OutputTruncated, ExitCode: outcome.ExitCode,
		FailureCode: failureCode, FailureMessage: failureMessage, Usage: outcome.Usage, FinishedAt: finishedAt,
	}
}

func normalizeExecutionOutcome(outcome ExecutionOutcome, maxOutputBytes int) ExecutionOutcome {
	validStatus := outcome.Status == api.ExecutionStatusSucceeded || outcome.Status == api.ExecutionStatusFailed ||
		outcome.Status == api.ExecutionStatusTimedOut || outcome.Status == api.ExecutionStatusOutOfMemory
	if !validStatus || (outcome.Status == api.ExecutionStatusSucceeded && len(outcome.Result) != 0 && !json.Valid(outcome.Result)) ||
		(outcome.Status != api.ExecutionStatusSucceeded && len(outcome.Result) != 0) ||
		(outcome.ExitCode != nil && (*outcome.ExitCode < 0 || *outcome.ExitCode > 255)) ||
		outcome.Usage.WallTimeMS < 0 || outcome.Usage.CPUTimeMS < 0 || outcome.Usage.PeakMemoryMB < 0 {
		return executionFailure("guest_protocol_error", "execution guest returned an invalid result")
	}
	if len(outcome.Result)+len(outcome.Stdout)+len(outcome.Stderr) > maxOutputBytes {
		return executionFailure("output_limit_exceeded", "execution output exceeded the admitted byte limit")
	}
	if outcome.Status == api.ExecutionStatusSucceeded {
		outcome.FailureCode = ""
		outcome.FailureMessage = ""
		return outcome
	}
	if len(outcome.FailureCode) == 0 || len(outcome.FailureCode) > 64 || len(outcome.FailureMessage) == 0 || len(outcome.FailureMessage) > 4096 {
		return executionFailure("guest_protocol_error", "execution guest returned invalid failure detail")
	}
	return outcome
}

func executionFailure(code, message string) ExecutionOutcome {
	return ExecutionOutcome{Status: api.ExecutionStatusFailed, FailureCode: code, FailureMessage: message}
}

func stringPointer(value string) *string {
	copy := value
	return &copy
}

func (c *ExecutionCoordinator) runWorker(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := c.ProcessNext(ctx)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, state.ErrExecutionLeaseLost) {
			c.log.Warn("schedd: execution worker failed", "err", err)
		}
		if processed && err == nil {
			continue
		}
		if !waitExecutionInterval(ctx, c.config.PollInterval) {
			return
		}
	}
}

func (c *ExecutionCoordinator) runSweeper(ctx context.Context) {
	ticker := time.NewTicker(c.config.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				c.log.Warn("schedd: execution recovery sweep failed", "err", err)
			}
		}
	}
}

func waitExecutionInterval(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
