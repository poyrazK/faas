package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) ReserveDataRepairRehearsal(ctx context.Context, t state.DataRepairRehearsal, nodeID string, runner core.RunnerSpec, incarnation string) (core.RehearsalFrame, error) {
	app, err := e.store.AppByID(ctx, t.Scope.AppID)
	if err != nil {
		return core.RehearsalFrame{}, err
	}
	if app.AccountID != t.AccountID || app.Status == state.AppDeleted || !e.ownsApp(app) {
		return core.RehearsalFrame{}, state.ErrConflict
	}
	account, err := e.store.AccountByID(ctx, t.AccountID)
	if err != nil {
		return core.RehearsalFrame{}, err
	}
	if !account.Active() || !api.MustLimitsFor(account.Plan).Operations.Allowed {
		return core.RehearsalFrame{}, state.ErrConflict
	}
	node, err := e.store.ComputeNodeByID(ctx, nodeID)
	if err != nil {
		return core.RehearsalFrame{}, err
	}
	if !node.Active {
		return core.RehearsalFrame{}, state.ErrConflict
	}
	f := core.RehearsalFrame{TaskID: t.ID, OperationID: t.OperationID, Scope: t.Scope, Binding: t.Binding, NodeID: nodeID, Incarnation: incarnation, PlanID: t.PlanID, PlanDigest: t.PlanDigest, ChecksSHA256: t.ChecksSHA256, PlanExpiresAt: t.PlanExpiresAt, Deadline: time.Now().UTC().Add(time.Duration(api.DataRepairTimeoutSeconds) * time.Second), Plan: account.Plan, Runner: runner}
	if f.Deadline.After(f.PlanExpiresAt) {
		f.Deadline = f.PlanExpiresAt
	}
	if err = f.Validate(); err != nil {
		return f, err
	}
	return f, e.reserveDataRepairFrame(ctx, f, false)
}

func (e *Engine) reserveDataRepairFrame(ctx context.Context, f core.RehearsalFrame, recovery bool) error {
	node, err := e.store.ComputeNodeByID(ctx, f.NodeID)
	if err != nil {
		return err
	}
	return e.ledger.Admit(Request{Instance: f.TaskID, AppID: f.Scope.AppID, Plan: f.Plan, RAMMB: api.DataRepairSandboxMemoryMB, VCPU: api.DataRepairSandboxVCPUs, CPUMillicores: api.DataRepairSandboxCPUMillicores, Kind: KindDataRepairRehearsal, NodeID: f.NodeID, NodeCeilingMB: node.AdmissionCeilingMB, VCPUBudget: node.VCPUBudget, CPUBudgetMillicores: int(cpuBudgetMillicores(node)), AllowCPUOvercommitRecovery: recovery})
}

func (e *Engine) ReleaseDataRepairRehearsal(id string) { e.ledger.Release(id) }

// Reconstruct capacity even when the feature's dispatch gate or bindings have
// been removed. A task lease expiring does not release its physical resources.
func (e *Engine) SeedDataRepairRehearsals(ctx context.Context) error {
	s, ok := e.store.(state.DataRepairRehearsalStore)
	if !ok {
		return nil
	}
	after := ""
	for {
		rows, err := s.ListDataRepairRehearsals(ctx, false, "", after, api.DataRepairRehearsalPageSize)
		if err != nil {
			return err
		}
		for _, t := range rows {
			after = t.ID
			if t.Frame != nil && t.Phase != state.RepairRehearsalPublishing {
				if err = e.reserveDataRepairFrame(ctx, *t.Frame, true); err != nil {
					return err
				}
			}
		}
		if len(rows) < api.DataRepairRehearsalPageSize {
			return nil
		}
	}
}
