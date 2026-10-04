package fcvm

// adr: 489
import (
	"context"
	"fmt"
)

// RuntimeAdmission identifies a host operation independently of request
// concurrency. App tasks, paused restores and migration resumes must also pass.
// Builder and disposable execution retain their own durable claim protocols.
type RuntimeAdmission struct {
	Instance, AppID, AccountID, DeploymentID       string
	AppTaskOnly, ExecutionOnly, IsBuilder, Command bool
}
type RuntimeAuthorizer func(context.Context, RuntimeAdmission) error

// WithRuntimeAuthorizer is startup-only wiring, before lifecycle RPCs begin.
// Production uses local durable state; it never queries the global directory.
func (m *Manager) WithRuntimeAuthorizer(authorize RuntimeAuthorizer) *Manager {
	m.runtimeAuthorizer = authorize
	return m
}
func admissionForWake(req WakeRequest) RuntimeAdmission {
	return RuntimeAdmission{Instance: req.Instance, AppID: req.AppID, AccountID: req.AccountID, DeploymentID: req.DeploymentID, AppTaskOnly: req.AppTaskOnly, ExecutionOnly: req.ExecutionOnly, IsBuilder: req.ExportDir != ""}
}
func admissionForInstance(inst *Instance) RuntimeAdmission {
	return RuntimeAdmission{Instance: inst.Lease.Instance, AppID: inst.AppID, AccountID: inst.AccountID, DeploymentID: inst.DeploymentID, AppTaskOnly: inst.AppTaskOnly, ExecutionOnly: inst.ExecutionOnly, IsBuilder: inst.Lease.IsBuilder}
}
func (m *Manager) authorizeRuntime(ctx context.Context, admission RuntimeAdmission) error {
	if m.runtimeAuthorizer == nil {
		return nil
	}
	if err := m.runtimeAuthorizer(ctx, admission); err != nil {
		return fmt.Errorf("runtime admission %s: %w", admission.Instance, err)
	}
	return nil
}
func (m *Manager) resumeAuthorized(ctx context.Context, inst *Instance) error {
	if err := m.authorizeRuntime(ctx, admissionForInstance(inst)); err != nil {
		return err
	}
	return m.vmm.ResumeVM(ctx, inst.Lease)
}
