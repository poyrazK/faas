package fcvm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/executionproto"
)

var (
	// ErrExecutionNotConfigured means this vmmd was started with a VMM that
	// does not expose the one-shot guest dialer. It is kept distinct from a
	// dead instance so callers can report a capability mismatch accurately.
	ErrExecutionNotConfigured = errors.New("fcvm: execution is not configured")
	// ErrExecutionInstanceNotFound is returned before any guest payload is
	// sent when the requested disposable instance is no longer live here.
	ErrExecutionInstanceNotFound = errors.New("fcvm: execution instance is not live")
)

const executionDestroyTimeout = 5 * time.Second

// ExecuteExecution performs the post-restore half of a one-shot execution.
// The instance must already have been restored or cold-booted by Wake; this
// method only dials the reserved guest listener, sends one validated request,
// and tears down both the VM and Manager bookkeeping before returning.
//
// Keeping this on Manager (rather than exposing live leases to gRPC) makes
// the instance lookup and cleanup atomic from the caller's point of view.
func (m *Manager) ExecuteExecution(ctx context.Context, instance string, req executionproto.Request) (executionproto.Result, error) {
	var zero executionproto.Result
	if err := req.Validate(); err != nil {
		return zero, err
	}
	if m == nil || m.vmm == nil {
		return zero, ErrExecutionNotConfigured
	}

	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok || inst == nil || !inst.ExecutionOnly {
		return zero, ErrExecutionInstanceNotFound
	}

	dialer, ok := m.vmm.(ExecutionDialer)
	if !ok {
		return zero, ErrExecutionNotConfigured
	}
	session, err := dialer.DialExecution(ctx, inst.Lease)
	if err != nil {
		// A failed dial cannot be retried safely: the guest may have accepted
		// the CONNECT and then disappeared. Release the live VM and lease.
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), executionDestroyTimeout)
		destroyErr := m.Destroy(destroyCtx, instance)
		cancel()
		if destroyErr != nil {
			return zero, errors.Join(fmt.Errorf("fcvm: dial execution: %w", err), fmt.Errorf("fcvm: destroy after dial failure: %w", destroyErr))
		}
		return zero, fmt.Errorf("fcvm: dial execution: %w", err)
	}
	if session == nil {
		nilSessionErr := errors.New("fcvm: execution dialer returned nil session")
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), executionDestroyTimeout)
		destroyErr := m.Destroy(destroyCtx, instance)
		cancel()
		if destroyErr != nil {
			return zero, errors.Join(nilSessionErr, fmt.Errorf("fcvm: destroy after nil execution session: %w", destroyErr))
		}
		return zero, nilSessionErr
	}

	result, executeErr := session.Execute(ctx, req)
	destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), executionDestroyTimeout)
	sessionDestroyErr := session.Destroy(destroyCtx)
	// ExecutionSession's destroy hook tears down Firecracker, while Manager
	// owns the netns/lease/live-map cleanup. Both are required by the parked =
	// zero-resource invariant; the second call is idempotent in JailerVMM.
	managerDestroyErr := m.Destroy(destroyCtx, instance)
	cancel()

	var errs []error
	if executeErr != nil {
		errs = append(errs, fmt.Errorf("fcvm: guest execution: %w", executeErr))
	}
	if sessionDestroyErr != nil {
		errs = append(errs, fmt.Errorf("fcvm: destroy execution session: %w", sessionDestroyErr))
	}
	if managerDestroyErr != nil {
		errs = append(errs, fmt.Errorf("fcvm: destroy execution instance: %w", managerDestroyErr))
	}
	if len(errs) > 0 {
		return zero, errors.Join(errs...)
	}
	return result, nil
}
