package sched

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// ResolvedAppTaskRuntime is the command-free result of resolving a task's
// immutable deployment pin into one vmmd wake envelope.
type ResolvedAppTaskRuntime struct {
	NodeID  string
	Spec    AppTaskRestoreSpec
	release func()
	retire  func(context.Context) error // Captured original source; never generic Destroy.
}

type AppTaskRuntimeResolver func(context.Context, AppTaskRestoreRequest) (ResolvedAppTaskRuntime, error)

// RoutedAppTaskVMM is the scheduler-side production transport. Restore and
// Execute are separate methods so command bytes cannot accidentally enter the
// preparation request.
type RoutedAppTaskVMM interface {
	RestoreAppTask(context.Context, string, AppTaskRestoreSpec) (*AppTaskRestoreOutcome, error)
	ExecuteAppTask(context.Context, string, string, apptaskproto.Request) (apptaskproto.Result, error)
	Destroy(context.Context, string, string) error
}

type RoutedVmmdAppTaskBackend struct {
	router  RoutedAppTaskVMM
	resolve AppTaskRuntimeResolver
}

func NewRoutedVmmdAppTaskBackend(router RoutedAppTaskVMM, resolve AppTaskRuntimeResolver) *RoutedVmmdAppTaskBackend {
	return &RoutedVmmdAppTaskBackend{router: router, resolve: resolve}
}

func (b *RoutedVmmdAppTaskBackend) Restore(ctx context.Context, request AppTaskRestoreRequest) (AppTaskSession, error) {
	if b == nil || b.router == nil || b.resolve == nil {
		return nil, ErrAppTaskCoordinatorNotWired
	}
	instance, err := runtimequota.AppTaskInstanceID(request.ID, request.LeaseToken)
	if err != nil {
		return nil, state.ErrAppTaskLeaseLost
	}
	resolved, err := b.resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	if resolved.NodeID == "" || resolved.Spec.Instance != instance || resolved.Spec.TaskID != request.ID ||
		resolved.Spec.RunID != request.LeaseToken || resolved.Spec.DeploymentID != request.DeploymentID {
		if resolved.Spec.App.RegionalRuntime == nil && resolved.release != nil {
			resolved.release()
		}
		return nil, errors.New("sched: app task resolver returned an invalid runtime identity")
	}
	if resolved.Spec.App.RegionalRuntime != nil && resolved.retire == nil {
		return nil, runtimequota.ErrTrust
	}
	outcome, err := b.router.RestoreAppTask(ctx, resolved.NodeID, resolved.Spec)
	if err != nil {
		cleanupErr := destroyRestoredAppTask(ctx, b.router, resolved)
		if cleanupErr != nil {
			return nil, errors.Join(err, cleanupErr)
		}
		return nil, err
	}
	if outcome == nil || outcome.Instance == "" || outcome.Instance != instance {
		cleanupErr := destroyRestoredAppTask(ctx, b.router, resolved)
		identityErr := errors.New("sched: vmmd returned an unexpected app task instance")
		if cleanupErr != nil {
			return nil, errors.Join(identityErr, cleanupErr)
		}
		return nil, identityErr
	}
	return &routedVmmdAppTaskSession{
		router: b.router, nodeID: resolved.NodeID, instance: outcome.Instance, taskID: request.ID,
		release: resolved.release, retire: resolved.retire, regional: resolved.Spec.App.RegionalRuntime != nil,
	}, nil
}

func destroyRestoredAppTask(parent context.Context, router RoutedAppTaskVMM, resolved ResolvedAppTaskRuntime) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), defaultAppTaskDestroyTimeout)
	defer cancel()
	if resolved.Spec.App.RegionalRuntime != nil {
		if resolved.retire == nil {
			return runtimequota.ErrTrust
		}
		return resolved.retire(cleanupCtx)
	}
	if err := router.Destroy(cleanupCtx, resolved.NodeID, resolved.Spec.Instance); err != nil {
		return err
	}
	if resolved.release != nil {
		resolved.release()
	}
	return nil
}

type routedVmmdAppTaskSession struct {
	router   RoutedAppTaskVMM
	nodeID   string
	instance string
	taskID   string
	release  func()
	retire   func(context.Context) error
	regional bool
	once     sync.Once
}

func (s *routedVmmdAppTaskSession) Execute(ctx context.Context, request AppTaskExecuteRequest) (AppTaskOutcome, error) {
	timeoutSeconds := int(math.Ceil(request.Timeout.Seconds()))
	wireReq := apptaskproto.Request{
		Version: apptaskproto.Version, TaskID: s.taskID,
		Command: append([]string(nil), request.Command...), CommandShell: request.CommandShell,
		TimeoutSeconds: timeoutSeconds, MaxOutputBytes: request.MaxOutputBytes,
	}
	if err := wireReq.Validate(); err != nil {
		return AppTaskOutcome{}, err
	}
	var result apptaskproto.Result
	var err error
	if streaming, ok := s.router.(interface {
		ExecuteAppTaskWithOutput(context.Context, string, string, apptaskproto.Request, apptaskproto.OutputReceiver) (apptaskproto.Result, error)
	}); ok {
		result, err = streaming.ExecuteAppTaskWithOutput(ctx, s.nodeID, s.instance, wireReq, nil)
	} else {
		result, err = s.router.ExecuteAppTask(ctx, s.nodeID, s.instance, wireReq)
	}
	if err != nil {
		return AppTaskOutcome{}, err
	}
	return appTaskOutcomeFromProtocol(result), nil
}

func (s *routedVmmdAppTaskSession) Destroy(ctx context.Context) error {
	if s.regional {
		if s.retire == nil {
			return runtimequota.ErrTrust
		}
		return s.retire(ctx)
	}
	if err := s.router.Destroy(ctx, s.nodeID, s.instance); err != nil {
		return err
	}
	s.once.Do(func() {
		if s.release != nil {
			s.release()
		}
	})
	return nil
}

func appTaskOutcomeFromProtocol(result apptaskproto.Result) AppTaskOutcome {
	status := state.AppTaskFailed
	switch result.Status {
	case apptaskproto.StatusSucceeded:
		status = state.AppTaskSucceeded
	case apptaskproto.StatusTimedOut:
		status = state.AppTaskTimedOut
	}
	return AppTaskOutcome{
		Status: status, StdoutTail: string(result.Stdout), StderrTail: string(result.Stderr),
		OutputTruncated: result.OutputTruncated, ExitCode: result.ExitCode,
		OutcomeCode: result.OutcomeCode,
		FailureCode: result.FailureCode, FailureMessage: result.FailureMessage,
	}
}
