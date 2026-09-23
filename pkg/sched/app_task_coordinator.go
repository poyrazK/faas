package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	DefaultAppTaskDispatchConcurrency = 1
	MaxAppTaskDispatchConcurrency     = 32
	defaultAppTaskLeaseDuration       = 30 * time.Second
	defaultAppTaskLeaseRenewInterval  = 10 * time.Second
	defaultAppTaskPollInterval        = 200 * time.Millisecond
	defaultAppTaskSweepInterval       = time.Second
	defaultAppTaskRestoreTimeout      = 2 * time.Minute
	defaultAppTaskDestroyTimeout      = 10 * time.Second
	defaultAppTaskFinalizeTimeout     = 5 * time.Second
)

var ErrAppTaskCoordinatorNotWired = errors.New("sched: app task coordinator is not fully wired")

// AppTaskCoordinatorConfig controls scheduler mechanics. Enabled remains an
// exact opt-in so the coordinator can land before the vmmd adapter without
// claiming durable work that no runtime can execute.
type AppTaskCoordinatorConfig struct {
	Enabled            bool
	Owner              string
	MaxConcurrent      int
	LeaseDuration      time.Duration
	LeaseRenewInterval time.Duration
	PollInterval       time.Duration
	SweepInterval      time.Duration
	RestoreTimeout     time.Duration
	DestroyTimeout     time.Duration
	FinalizeTimeout    time.Duration
}

// AppTaskRestoreRequest deliberately excludes the command. A backend may
// restore the pinned deployment and resolve its scoped runtime environment,
// but customer code cannot be dispatched before MarkAppTaskRunning wins.
type AppTaskRestoreRequest struct {
	ID              string
	AccountID       string
	AppID           string
	DeploymentID    string
	Kind            state.AppTaskKind
	DeploymentScope string
	ArtifactKey     string
	ImageDigest     string
}

// AppTaskExecuteRequest is handed to an already-restored session only after
// the durable running fence has been written.
type AppTaskExecuteRequest struct {
	Command        []string
	CommandShell   bool
	Timeout        time.Duration
	MaxOutputBytes int
}

// AppTaskOutcome is the bounded terminal projection returned by the runtime.
// The coordinator validates it before any value reaches durable state.
type AppTaskOutcome struct {
	Status          state.AppTaskStatus
	StdoutTail      string
	StderrTail      string
	OutputTruncated bool
	ExitCode        *int
	FailureCode     string
	FailureMessage  string
}

// AppTaskSession is one fresh deployment-attached VM. Execute must honor ctx
// cancellation and Destroy must be idempotent.
type AppTaskSession interface {
	Execute(context.Context, AppTaskExecuteRequest) (AppTaskOutcome, error)
	Destroy(context.Context) error
}

// AppTaskBackend restores a task VM without dispatching its command. If
// Restore returns an error it owns cleanup of any partial allocation.
type AppTaskBackend interface {
	Restore(context.Context, AppTaskRestoreRequest) (AppTaskSession, error)
}

// AppTaskCoordinator owns claim, restore, dispatch fencing, cancellation,
// teardown, and terminal acknowledgement for deployment-attached commands.
type AppTaskCoordinator struct {
	store   state.AppTaskStore
	backend AppTaskBackend
	config  AppTaskCoordinatorConfig
	log     *slog.Logger
	now     func() time.Time
}

func NewAppTaskCoordinator(store state.AppTaskStore, backend AppTaskBackend, config AppTaskCoordinatorConfig, log *slog.Logger) *AppTaskCoordinator {
	if log == nil {
		log = slog.Default()
	}
	return &AppTaskCoordinator{
		store: store, backend: backend, config: normalizeAppTaskCoordinatorConfig(config), log: log, now: time.Now,
	}
}

func normalizeAppTaskCoordinatorConfig(config AppTaskCoordinatorConfig) AppTaskCoordinatorConfig {
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		config.Owner = "schedd"
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = DefaultAppTaskDispatchConcurrency
	}
	if config.MaxConcurrent > MaxAppTaskDispatchConcurrency {
		config.MaxConcurrent = MaxAppTaskDispatchConcurrency
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = defaultAppTaskLeaseDuration
	}
	if config.LeaseRenewInterval <= 0 || config.LeaseRenewInterval >= config.LeaseDuration {
		config.LeaseRenewInterval = config.LeaseDuration / 3
	}
	if config.LeaseRenewInterval <= 0 {
		config.LeaseRenewInterval = defaultAppTaskLeaseRenewInterval
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultAppTaskPollInterval
	}
	if config.SweepInterval <= 0 {
		config.SweepInterval = defaultAppTaskSweepInterval
	}
	if config.RestoreTimeout <= 0 {
		config.RestoreTimeout = defaultAppTaskRestoreTimeout
	}
	if config.DestroyTimeout <= 0 {
		config.DestroyTimeout = defaultAppTaskDestroyTimeout
	}
	if config.FinalizeTimeout <= 0 {
		config.FinalizeTimeout = defaultAppTaskFinalizeTimeout
	}
	return config
}

// Run performs recovery before starting a bounded worker pool and sweeper.
func (c *AppTaskCoordinator) Run(ctx context.Context) error {
	if c == nil || !c.config.Enabled {
		return nil
	}
	if c.store == nil || c.backend == nil {
		return ErrAppTaskCoordinatorNotWired
	}
	if _, err := c.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		c.log.Warn("schedd: app task initial recovery sweep failed", "error_class", appTaskErrorClass(err))
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

// ProcessNext claims and fully handles at most one task.
func (c *AppTaskCoordinator) ProcessNext(ctx context.Context) (bool, error) {
	if c == nil || c.store == nil || c.backend == nil {
		return false, ErrAppTaskCoordinatorNotWired
	}
	task, err := c.store.ClaimNextAppTask(ctx, c.config.Owner, c.now().UTC(), c.config.LeaseDuration)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sched: claim app task: %w", err)
	}
	if task.LeaseToken == nil || *task.LeaseToken == "" {
		return true, fmt.Errorf("sched: claimed app task %s without lease token", task.ID)
	}
	return true, c.processClaim(ctx, task)
}

// SweepOnce recovers expired restores and terminalizes expired running tasks.
func (c *AppTaskCoordinator) SweepOnce(ctx context.Context) (state.AppTaskSweepResult, error) {
	if c == nil || c.store == nil {
		return state.AppTaskSweepResult{}, ErrAppTaskCoordinatorNotWired
	}
	result, err := c.store.SweepExpiredAppTasks(ctx, c.now().UTC())
	if err != nil {
		return state.AppTaskSweepResult{}, fmt.Errorf("sched: sweep app tasks: %w", err)
	}
	return result, nil
}

type appTaskLeaseSignal uint8

const (
	appTaskLeaseHealthy appTaskLeaseSignal = iota
	appTaskLeaseCancelled
	appTaskLeaseLost
)

func (c *AppTaskCoordinator) processClaim(parent context.Context, task state.AppTask) error {
	workCtx, cancelWork := context.WithCancel(parent)
	defer cancelWork()
	monitorCtx, stopMonitor := context.WithCancel(parent)
	signals := make(chan appTaskLeaseSignal, 1)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		c.monitorLease(monitorCtx, task, cancelWork, signals)
	}()
	stopLeaseMonitor := func() appTaskLeaseSignal {
		stopMonitor()
		<-monitorDone
		return latestAppTaskLeaseSignal(signals)
	}

	restoreCtx, cancelRestore := context.WithTimeout(workCtx, c.config.RestoreTimeout)
	session, restoreErr := c.backend.Restore(restoreCtx, AppTaskRestoreRequest{
		ID: task.ID, AccountID: task.AccountID, AppID: task.AppID,
		DeploymentID: task.DeploymentID, Kind: task.Kind, DeploymentScope: task.DeploymentScope,
		ArtifactKey: task.ArtifactKey, ImageDigest: task.ImageDigest,
	})
	restoreTimedOut := errors.Is(restoreCtx.Err(), context.DeadlineExceeded)
	cancelRestore()
	if restoreErr != nil || session == nil || restoreTimedOut {
		if restoreErr == nil && restoreTimedOut {
			restoreErr = context.DeadlineExceeded
		} else if restoreErr == nil && session == nil {
			restoreErr = errors.New("app task backend returned a nil session")
		}
		if session != nil {
			if err := c.destroy(parent, task.ID, session); err != nil {
				stopLeaseMonitor()
				return err
			}
		}
		signal := stopLeaseMonitor()
		if handled, err := c.finishInterrupted(parent, task, signal); handled {
			return err
		}
		code, message := "restore_failed", "app task environment could not be prepared"
		if restoreTimedOut {
			code, message = "restore_timeout", "app task environment preparation timed out"
		}
		c.log.Warn("schedd: app task restore failed", "task_id", task.ID, "error_class", appTaskErrorClass(restoreErr))
		return c.complete(parent, task, appTaskFailure(state.AppTaskFailed, code, message), c.now().UTC())
	}

	startedAt := c.now().UTC()
	if _, err := c.store.MarkAppTaskRunning(parent, task.ID, *task.LeaseToken, startedAt); err != nil {
		destroyErr := c.destroy(parent, task.ID, session)
		signal := stopLeaseMonitor()
		if destroyErr != nil {
			return destroyErr
		}
		if signal == appTaskLeaseHealthy && errors.Is(err, state.ErrAppTaskLeaseLost) {
			signal = c.currentLeaseSignal(parent, task)
		}
		if handled, interruptedErr := c.finishInterrupted(parent, task, signal); handled {
			return interruptedErr
		}
		return fmt.Errorf("sched: mark app task %s running: %w", task.ID, err)
	}

	executeCtx, cancelExecute := context.WithTimeout(workCtx, time.Duration(task.TimeoutSeconds)*time.Second)
	outcome, executeErr := session.Execute(executeCtx, AppTaskExecuteRequest{
		Command: append([]string(nil), task.Command...), CommandShell: task.CommandShell,
		Timeout: time.Duration(task.TimeoutSeconds) * time.Second, MaxOutputBytes: task.MaxOutputBytes,
	})
	executeTimedOut := errors.Is(executeCtx.Err(), context.DeadlineExceeded)
	cancelExecute()
	destroyErr := c.destroy(parent, task.ID, session)
	signal := stopLeaseMonitor()
	if destroyErr != nil {
		return destroyErr
	}
	if handled, err := c.finishInterrupted(parent, task, signal); handled {
		return err
	}
	if executeTimedOut {
		outcome = appTaskFailure(state.AppTaskTimedOut, "timeout", "app task command timed out")
	} else if executeErr != nil {
		c.log.Warn("schedd: app task execution transport failed", "task_id", task.ID, "error_class", appTaskErrorClass(executeErr))
		outcome = appTaskFailure(state.AppTaskFailed, "execution_transport_failed", "app task result channel closed unexpectedly")
	}
	outcome = normalizeAppTaskOutcome(outcome, task.MaxOutputBytes)
	return c.complete(parent, task, outcome, c.now().UTC())
}

func (c *AppTaskCoordinator) monitorLease(ctx context.Context, task state.AppTask, cancelWork context.CancelFunc, signals chan<- appTaskLeaseSignal) {
	ticker := time.NewTicker(c.config.LeaseRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := c.store.RenewAppTaskLease(ctx, task.ID, *task.LeaseToken, c.now().UTC(), c.config.LeaseDuration)
			if err == nil {
				continue
			}
			signal := appTaskLeaseLost
			if errors.Is(err, state.ErrAppTaskLeaseLost) {
				signal = c.currentLeaseSignal(ctx, task)
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

func (c *AppTaskCoordinator) currentLeaseSignal(ctx context.Context, task state.AppTask) appTaskLeaseSignal {
	row, err := c.store.AppTaskByID(ctx, task.AccountID, task.AppID, task.ID)
	if err == nil && row.CancelRequested != nil && row.LeaseToken != nil && task.LeaseToken != nil && *row.LeaseToken == *task.LeaseToken {
		return appTaskLeaseCancelled
	}
	return appTaskLeaseLost
}

func latestAppTaskLeaseSignal(signals <-chan appTaskLeaseSignal) appTaskLeaseSignal {
	select {
	case signal := <-signals:
		return signal
	default:
		return appTaskLeaseHealthy
	}
}

func (c *AppTaskCoordinator) finishInterrupted(parent context.Context, task state.AppTask, signal appTaskLeaseSignal) (bool, error) {
	if parent.Err() != nil {
		return true, parent.Err()
	}
	switch signal {
	case appTaskLeaseCancelled:
		return true, c.complete(parent, task, AppTaskOutcome{Status: state.AppTaskCancelled}, c.now().UTC())
	case appTaskLeaseLost:
		return true, state.ErrAppTaskLeaseLost
	default:
		return false, nil
	}
}

func (c *AppTaskCoordinator) destroy(parent context.Context, taskID string, session AppTaskSession) error {
	destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), c.config.DestroyTimeout)
	defer cancel()
	if err := session.Destroy(destroyCtx); err != nil {
		c.log.Error("schedd: app task teardown failed; terminal acknowledgement withheld", "task_id", taskID, "error_class", appTaskErrorClass(err))
		return fmt.Errorf("sched: destroy app task %s: %w", taskID, err)
	}
	return nil
}

func (c *AppTaskCoordinator) complete(parent context.Context, task state.AppTask, outcome AppTaskOutcome, finishedAt time.Time) error {
	completeCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), c.config.FinalizeTimeout)
	defer cancel()
	params := appTaskCompletionParams(task, outcome, finishedAt)
	_, err := c.store.CompleteAppTask(completeCtx, params)
	if errors.Is(err, state.ErrAppTaskCancellationPending) {
		_, err = c.store.CompleteAppTask(completeCtx, appTaskCompletionParams(
			task, AppTaskOutcome{Status: state.AppTaskCancelled}, c.now().UTC(),
		))
	}
	if err != nil {
		return fmt.Errorf("sched: complete app task %s: %w", task.ID, err)
	}
	return nil
}

func appTaskCompletionParams(task state.AppTask, outcome AppTaskOutcome, finishedAt time.Time) state.CompleteAppTaskParams {
	var failureCode, failureMessage *string
	if outcome.FailureCode != "" || outcome.FailureMessage != "" {
		failureCode = stringPointer(outcome.FailureCode)
		failureMessage = stringPointer(outcome.FailureMessage)
	}
	return state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *task.LeaseToken, Status: outcome.Status,
		StdoutTail: outcome.StdoutTail, StderrTail: outcome.StderrTail,
		OutputTruncated: outcome.OutputTruncated, ExitCode: outcome.ExitCode,
		FailureCode: failureCode, FailureMessage: failureMessage, FinishedAt: finishedAt,
	}
}

func normalizeAppTaskOutcome(outcome AppTaskOutcome, maxOutputBytes int) AppTaskOutcome {
	validStatus := outcome.Status == state.AppTaskSucceeded || outcome.Status == state.AppTaskFailed || outcome.Status == state.AppTaskTimedOut
	validExit := outcome.ExitCode == nil || (*outcome.ExitCode >= 0 && *outcome.ExitCode <= 255)
	if !validStatus || !validExit {
		return appTaskFailure(state.AppTaskFailed, "guest_protocol_error", "app task guest returned an invalid result")
	}
	if outcome.Status == state.AppTaskSucceeded {
		if outcome.ExitCode == nil || *outcome.ExitCode != 0 || outcome.FailureCode != "" || outcome.FailureMessage != "" {
			return appTaskFailure(state.AppTaskFailed, "guest_protocol_error", "app task guest returned an invalid success result")
		}
	} else if len(outcome.FailureCode) == 0 || len(outcome.FailureCode) > 64 || len(outcome.FailureMessage) == 0 || len(outcome.FailureMessage) > 4096 {
		return appTaskFailure(state.AppTaskFailed, "guest_protocol_error", "app task guest returned invalid failure detail")
	}
	outcome.StdoutTail, outcome.StderrTail, outcome.OutputTruncated = boundAppTaskOutput(
		outcome.StdoutTail, outcome.StderrTail, maxOutputBytes, outcome.OutputTruncated,
	)
	return outcome
}

func appTaskFailure(status state.AppTaskStatus, code, message string) AppTaskOutcome {
	return AppTaskOutcome{Status: status, FailureCode: code, FailureMessage: message}
}

func boundAppTaskOutput(stdout, stderr string, maxBytes int, alreadyTruncated bool) (string, string, bool) {
	stdout = strings.ToValidUTF8(stdout, "\uFFFD")
	stderr = strings.ToValidUTF8(stderr, "\uFFFD")
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(stdout)+len(stderr) <= maxBytes {
		return stdout, stderr, alreadyTruncated
	}
	stdoutBudget := maxBytes / 2
	stderrBudget := maxBytes - stdoutBudget
	if len(stdout) < stdoutBudget {
		stderrBudget += stdoutBudget - len(stdout)
		stdoutBudget = len(stdout)
	}
	if len(stderr) < stderrBudget {
		stdoutBudget += stderrBudget - len(stderr)
		stderrBudget = len(stderr)
	}
	return tailUTF8(stdout, stdoutBudget), tailUTF8(stderr, stderrBudget), true
}

func tailUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func (c *AppTaskCoordinator) runWorker(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := c.ProcessNext(ctx)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, state.ErrAppTaskLeaseLost) {
			c.log.Warn("schedd: app task worker failed", "error_class", appTaskErrorClass(err))
		}
		if processed && err == nil {
			continue
		}
		if !waitAppTaskInterval(ctx, c.config.PollInterval) {
			return
		}
	}
}

func (c *AppTaskCoordinator) runSweeper(ctx context.Context) {
	ticker := time.NewTicker(c.config.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				c.log.Warn("schedd: app task recovery sweep failed", "error_class", appTaskErrorClass(err))
			}
		}
	}
}

func waitAppTaskInterval(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func appTaskErrorClass(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, state.ErrAppTaskLeaseLost):
		return "lease_lost"
	default:
		return "internal"
	}
}
