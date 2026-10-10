package fcvm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

var (
	ErrAppTaskNotConfigured    = errors.New("fcvm: app task is not configured")
	ErrAppTaskInstanceNotFound = errors.New("fcvm: app task instance is not live")
)

const appTaskDestroyTimeout = 5 * time.Second

func (m *Manager) ExecuteAppTask(ctx context.Context, instance string, req apptaskproto.Request) (apptaskproto.Result, error) {
	return m.ExecuteAppTaskWithOutput(ctx, instance, req, nil)
}

// ExecuteAppTaskWithOutput dispatches one command to an already-booted task
// guest and always tears down the VM, netns, lease, and live-map entry before
// returning. Ordinary app and disposable source-execution instances fail the
// AppTaskOnly fence before any payload is sent.
func (m *Manager) ExecuteAppTaskWithOutput(ctx context.Context, instance string, req apptaskproto.Request, receive apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	if err := req.Validate(); err != nil {
		return apptaskproto.Result{}, err
	}
	if req.Interactive {
		return apptaskproto.Result{}, fmt.Errorf("%w: interactive request requires an attached client", apptaskproto.ErrInvalidRequest)
	}
	return m.runAppTask(ctx, instance, req.TimeoutSeconds, func(requestCtx context.Context, session *AppTaskSession) (apptaskproto.Result, error) {
		return session.ExecuteWithOutput(requestCtx, req, receive)
	})
}

// ExecuteInteractiveAppTask runs one interactive session (ADR-958) against an
// already-booted task guest with the same AppTaskOnly fence and unconditional
// teardown as ExecuteAppTaskWithOutput.
func (m *Manager) ExecuteInteractiveAppTask(ctx context.Context, instance string, req apptaskproto.Request, input <-chan apptaskproto.InputEvent, receive apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	if err := req.Validate(); err != nil {
		return apptaskproto.Result{}, err
	}
	if !req.Interactive {
		return apptaskproto.Result{}, fmt.Errorf("%w: interactive request required", apptaskproto.ErrInvalidRequest)
	}
	return m.runAppTask(ctx, instance, req.TimeoutSeconds, func(requestCtx context.Context, session *AppTaskSession) (apptaskproto.Result, error) {
		return session.Interact(requestCtx, req, input, receive)
	})
}

func (m *Manager) runAppTask(ctx context.Context, instance string, timeoutSeconds int, run func(context.Context, *AppTaskSession) (apptaskproto.Result, error)) (apptaskproto.Result, error) {
	var zero apptaskproto.Result
	if m == nil || m.vmm == nil {
		return zero, ErrAppTaskNotConfigured
	}
	requestCtx, cancelRequest := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancelRequest()

	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok || inst == nil || !inst.AppTaskOnly {
		return zero, ErrAppTaskInstanceNotFound
	}
	dialer, ok := m.vmm.(AppTaskDialer)
	if !ok {
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), appTaskDestroyTimeout)
		destroyErr := m.Destroy(destroyCtx, instance)
		cancel()
		if destroyErr != nil {
			return zero, errors.Join(ErrAppTaskNotConfigured, fmt.Errorf("fcvm: destroy unsupported app task instance: %w", destroyErr))
		}
		return zero, ErrAppTaskNotConfigured
	}
	session, err := dialer.DialAppTask(requestCtx, inst.Lease)
	if err != nil {
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), appTaskDestroyTimeout)
		destroyErr := m.Destroy(destroyCtx, instance)
		cancel()
		if destroyErr != nil {
			return zero, errors.Join(fmt.Errorf("fcvm: dial app task: %w", err), fmt.Errorf("fcvm: destroy after dial failure: %w", destroyErr))
		}
		return zero, fmt.Errorf("fcvm: dial app task: %w", err)
	}
	if session == nil {
		nilSessionErr := errors.New("fcvm: app task dialer returned nil session")
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), appTaskDestroyTimeout)
		destroyErr := m.Destroy(destroyCtx, instance)
		cancel()
		if destroyErr != nil {
			return zero, errors.Join(nilSessionErr, fmt.Errorf("fcvm: destroy after nil app task session: %w", destroyErr))
		}
		return zero, nilSessionErr
	}

	result, executeErr := run(requestCtx, session)
	destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), appTaskDestroyTimeout)
	sessionDestroyErr := session.Destroy(destroyCtx)
	managerDestroyErr := m.Destroy(destroyCtx, instance)
	cancel()
	var errs []error
	if executeErr != nil {
		errs = append(errs, fmt.Errorf("fcvm: guest app task: %w", executeErr))
	}
	if sessionDestroyErr != nil {
		errs = append(errs, fmt.Errorf("fcvm: destroy app task session: %w", sessionDestroyErr))
	}
	if managerDestroyErr != nil {
		errs = append(errs, fmt.Errorf("fcvm: destroy app task instance: %w", managerDestroyErr))
	}
	if len(errs) > 0 {
		return zero, errors.Join(errs...)
	}
	return result, nil
}
