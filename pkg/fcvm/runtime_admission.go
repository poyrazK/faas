package fcvm

// adr: 489
import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

// RuntimeAdmission identifies a host operation independently of request
// concurrency. App tasks, paused restores and migration resumes must also pass.
// Builder and disposable execution retain their own durable claim protocols.
type RuntimeAdmission struct {
	Instance, AppID, AccountID, DeploymentID       string
	AppTaskOnly, ExecutionOnly, IsBuilder, Command bool
	RegionalRuntime                                *runtimequota.Placement
	KeepPaused                                     bool
}
type RuntimeAuthorizer func(context.Context, RuntimeAdmission) error

// WithRuntimeAuthorizer is startup-only wiring, before lifecycle RPCs begin.
// Production uses local durable state; it never queries the global directory.
func (m *Manager) WithRuntimeAuthorizer(authorize RuntimeAuthorizer) *Manager {
	m.runtimeAuthorizer = authorize
	return m
}
func admissionForWake(req WakeRequest) RuntimeAdmission {
	return RuntimeAdmission{Instance: req.Instance, AppID: req.AppID, AccountID: req.AccountID, DeploymentID: req.DeploymentID, AppTaskOnly: req.AppTaskOnly, ExecutionOnly: req.ExecutionOnly, IsBuilder: req.ExportDir != "", RegionalRuntime: cloneRuntimePlacement(req.RegionalRuntime), KeepPaused: req.KeepPaused}
}
func admissionForInstance(inst *Instance) RuntimeAdmission {
	return RuntimeAdmission{Instance: inst.Lease.Instance, AppID: inst.AppID, AccountID: inst.AccountID, DeploymentID: inst.DeploymentID, AppTaskOnly: inst.AppTaskOnly, ExecutionOnly: inst.ExecutionOnly, IsBuilder: inst.Lease.IsBuilder, RegionalRuntime: cloneRuntimePlacement(inst.regionalRuntime)}
}
func (m *Manager) authorizeRuntimeOwner(ctx context.Context, admission RuntimeAdmission) error {
	if err := validateRuntimePlacement(admission); err != nil {
		return err
	}
	if m.runtimeAuthorizer == nil {
		if admission.RegionalRuntime != nil {
			return runtimequota.ErrTrust
		}
		return nil
	}
	if err := m.runtimeAuthorizer(ctx, admission); err != nil {
		return fmt.Errorf("runtime admission %s: %w", admission.Instance, err)
	}
	return nil
}

// WithRuntimeHostJournal is startup-only wiring. The journal never replaces
// the cell authorizer; both must allow every regional execution boundary.
func (m *Manager) WithRuntimeHostJournal(journal *runtimequotahost.Journal) *Manager {
	m.runtimeHostJournal = journal
	return m
}

// RegionalRuntimeHostEpoch is discovery only. The cell authorizer and host
// journal still enforce every execution boundary independently of this sample.
func (m *Manager) RegionalRuntimeHostEpoch() (runtimequota.HostEpoch, error) {
	if m.runtimeHostJournal == nil {
		return runtimequota.HostEpoch{}, runtimequotahost.ErrStorage
	}
	return m.runtimeHostJournal.RuntimeHostEpoch()
}

func cloneRuntimePlacement(p *runtimequota.Placement) *runtimequota.Placement {
	if p == nil {
		return nil
	}
	copy := p.Clone()
	return &copy
}

func validateRuntimePlacement(a RuntimeAdmission) error {
	if a.RegionalRuntime == nil {
		return nil
	}
	p := *a.RegionalRuntime
	if p.Validate() != nil || a.IsBuilder || a.ExecutionOnly || p.Permit.InstanceID != a.Instance || p.Permit.DeploymentID != a.DeploymentID || p.Permit.Grant.Binding.RegionalAppID != a.AppID || p.Permit.Grant.Binding.RegionalAccountID != a.AccountID || (p.Permit.Kind == runtimequota.CapacityAppTask) != a.AppTaskOnly {
		return runtimequotahost.ErrBinding
	}
	if a.KeepPaused {
		if p.Permit.Kind != runtimequota.CapacityWarm || p.Seat != nil {
			return runtimequotahost.ErrBinding
		}
	} else if p.Permit.Kind == runtimequota.CapacityServing || p.Permit.Kind == runtimequota.CapacityWarm {
		if p.Seat == nil {
			return runtimequotahost.ErrBinding
		}
	}
	return nil
}

func (m *Manager) authorizeRuntime(ctx context.Context, a RuntimeAdmission) error {
	if err := m.authorizeRuntimeOwner(ctx, a); err != nil {
		return err
	}
	if a.RegionalRuntime == nil {
		return nil
	}
	if m.runtimeHostJournal == nil {
		return runtimequotahost.ErrStorage
	}
	return m.runtimeHostJournal.Check(*a.RegionalRuntime)
}

func (m *Manager) beginRuntimeOperation(ctx context.Context, a RuntimeAdmission) (func(), error) {
	return m.beginRuntimeOperationMode(ctx, a, false)
}

func (m *Manager) beginRuntimeLaunch(ctx context.Context, a RuntimeAdmission) (func(), error) {
	return m.beginRuntimeOperationMode(ctx, a, true)
}

func (m *Manager) beginRuntimeOperationMode(ctx context.Context, a RuntimeAdmission, launch bool) (func(), error) {
	if err := m.authorizeRuntimeOwner(ctx, a); err != nil {
		return nil, err
	}
	if a.RegionalRuntime == nil {
		return func() {}, nil
	}
	if m.runtimeHostJournal == nil {
		return nil, runtimequotahost.ErrStorage
	}
	if launch {
		return m.runtimeHostJournal.BeginLaunch(ctx, *a.RegionalRuntime)
	}
	return m.runtimeHostJournal.Begin(ctx, *a.RegionalRuntime)
}

func (m *Manager) resumeAuthorized(ctx context.Context, inst *Instance) error {
	finish, err := m.beginRuntimeOperation(ctx, admissionForInstance(inst))
	if err != nil {
		return err
	}
	defer finish()
	return m.vmm.ResumeVM(ctx, inst.Lease)
}

// Warm capture can still pause/snapshot an independent frozen app. Its
// existing early/error resume hooks perform execution authorization. Regional
// captures additionally hold the local barrier across those embedded resumes.
func (m *Manager) beginRuntimeSnapshot(ctx context.Context, a RuntimeAdmission) (func(), error) {
	if a.RegionalRuntime == nil {
		return func() {}, nil
	}
	return m.beginRuntimeOperation(ctx, a)
}
