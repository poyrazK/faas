package sched

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/state"
)

// ResolvedAppTaskRuntime is the command-free result of resolving a task's
// immutable deployment pin into one vmmd wake envelope.
type ResolvedAppTaskRuntime struct {
	NodeID  string
	Spec    AppTaskRestoreSpec
	release func()
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
	resolved, err := b.resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	if resolved.NodeID == "" || resolved.Spec.Instance != request.ID || resolved.Spec.DeploymentID != request.DeploymentID {
		if resolved.release != nil {
			resolved.release()
		}
		return nil, errors.New("sched: app task resolver returned an invalid runtime identity")
	}
	outcome, err := b.router.RestoreAppTask(ctx, resolved.NodeID, resolved.Spec)
	if err != nil {
		cleanupErr := destroyRestoredAppTask(ctx, b.router, resolved.NodeID, request.ID)
		if cleanupErr == nil && resolved.release != nil {
			resolved.release()
		}
		if cleanupErr != nil {
			return nil, errors.Join(err, cleanupErr)
		}
		return nil, err
	}
	if outcome == nil || outcome.Instance == "" || outcome.Instance != request.ID {
		cleanupErr := destroyRestoredAppTask(ctx, b.router, resolved.NodeID, request.ID)
		identityErr := errors.New("sched: vmmd returned an unexpected app task instance")
		if cleanupErr != nil {
			return nil, errors.Join(identityErr, cleanupErr)
		}
		if resolved.release != nil {
			resolved.release()
		}
		return nil, identityErr
	}
	return &routedVmmdAppTaskSession{
		router: b.router, nodeID: resolved.NodeID, instance: outcome.Instance, release: resolved.release,
	}, nil
}

func destroyRestoredAppTask(parent context.Context, router RoutedAppTaskVMM, nodeID, instance string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), defaultAppTaskDestroyTimeout)
	defer cancel()
	return router.Destroy(cleanupCtx, nodeID, instance)
}

type routedVmmdAppTaskSession struct {
	router   RoutedAppTaskVMM
	nodeID   string
	instance string
	release  func()
	once     sync.Once
}

func (s *routedVmmdAppTaskSession) Execute(ctx context.Context, request AppTaskExecuteRequest) (AppTaskOutcome, error) {
	timeoutSeconds := int(math.Ceil(request.Timeout.Seconds()))
	wireReq := apptaskproto.Request{
		Version: apptaskproto.Version, TaskID: s.instance,
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
		FailureCode: result.FailureCode, FailureMessage: result.FailureMessage,
	}
}
