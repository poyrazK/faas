package fcvm

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

// ErrJobSessionInstanceNotFound means no live interactive job VM has the
// instance id.
var ErrJobSessionInstanceNotFound = errors.New("fcvm: interactive job instance is not live")

// ExecuteInteractiveJob runs ADR-1330's one session against a live
// interactive job guest. Unlike an app task it never tears the VM down: the
// guest reports the job exit and powers off, and the job exit path destroys
// the VM.
func (m *Manager) ExecuteInteractiveJob(ctx context.Context, instance string, req apptaskproto.Request, input <-chan apptaskproto.InputEvent, receive apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	var zero apptaskproto.Result
	if err := req.Validate(); err != nil {
		return zero, err
	}
	if !req.Interactive {
		return zero, fmt.Errorf("%w: interactive request required", apptaskproto.ErrInvalidRequest)
	}
	if m == nil || m.vmm == nil {
		return zero, ErrAppTaskNotConfigured
	}
	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok || inst == nil || !inst.IsJob || !inst.JobInteractive {
		return zero, ErrJobSessionInstanceNotFound
	}
	dialer, ok := m.vmm.(JobSessionDialer)
	if !ok {
		return zero, ErrAppTaskNotConfigured
	}
	session, err := dialer.DialJobSession(ctx, inst.Lease)
	if err != nil {
		return zero, fmt.Errorf("fcvm: dial job session: %w", err)
	}
	if session == nil {
		return zero, errors.New("fcvm: job session dialer returned nil session")
	}
	defer func() { _ = session.Destroy(context.WithoutCancel(ctx)) }()
	return session.Interact(ctx, req, input, receive)
}
