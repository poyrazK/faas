package sched

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) serviceCapacityPlacementNodes(ctx context.Context, r Request, app state.App, nodes []state.ComputeNode, requiredRegion string) ([]state.ComputeNode, error) {
	if r.placementOnly {
		return nodes, nil
	}
	capacity, err := e.store.ServiceCapacityPlacement(ctx)
	if err != nil {
		return nil, fmt.Errorf("sched: placement: read service recovery capacity: %w", err)
	}
	if !capacity.Enabled {
		return nodes, nil
	}
	service := !r.ordinaryResident && app.Manifest.ExecutionMode == api.ExecutionModeService && (app.Status == state.AppActive || app.Status == state.AppEvictedCold)
	var ram, cpu, vcpu int64
	if !service {
		ram, cpu, vcpu, err = e.ordinaryPlacementResources(ctx, r, app)
		if err != nil {
			return nil, err
		}
	}
	eligible := make([]state.ComputeNode, 0, len(nodes))
	for _, node := range nodes {
		if !service {
			if capacity.AllowsOrdinary(node.ID, ram, cpu, vcpu) {
				eligible = append(eligible, node)
			}
			continue
		}
		if slots, healthy := capacity.Nodes[node.ID]; healthy && slots.Used < slots.Slots && ((requiredRegion != "" && slots.PinnedAllowed) || (requiredRegion == "" && slots.LegacyAllowed)) {
			eligible = append(eligible, node)
		}
	}
	return eligible, nil
}

// Match the durable certificate's insert-time resource bounds. Physical
// placement separately checks startup CPU boost and the live node budgets.
func (e *Engine) ordinaryPlacementResources(ctx context.Context, r Request, app state.App) (ram, cpu, vcpu int64, err error) {
	ram = int64(r.RAMMB) + api.PerVMOverheadMB
	cpu = int64(max(app.CPUMillicores, api.DefaultAppCPUMillicores))
	vcpu = 1 // standalone jobs have no app/account topology bound
	if r.AppID == "" {
		return ram, cpu, vcpu, nil
	}
	if r.DeploymentID == "" {
		return 0, 0, 0, api.ErrCapacity("placement: deployment is required for protected ordinary admission")
	}
	dep, err := e.store.DeploymentByID(ctx, r.DeploymentID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("sched: placement: read deployment resources: %w", err)
	}
	if dep.AppID != app.ID {
		return 0, 0, 0, api.ErrCapacity("placement: deployment does not belong to app")
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("sched: placement: read account topology: %w", err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	vcpu = int64(limits.VCPU)
	var sidecars []api.Sidecar
	if len(dep.Sidecars) != 0 {
		if err := json.Unmarshal(dep.Sidecars, &sidecars); err != nil {
			return 0, 0, 0, fmt.Errorf("sched: placement: decode sidecar resources: %w", err)
		}
	}
	for _, sidecar := range sidecars {
		ram += int64(max(0, sidecar.RamMB))
		sideCPU := sidecar.CPUMillicores
		if sideCPU <= 0 {
			sideCPU = api.DefaultAppCPUMillicores
		}
		cpu += int64(sideCPU)
	}
	return ram, cpu, vcpu, nil
}
