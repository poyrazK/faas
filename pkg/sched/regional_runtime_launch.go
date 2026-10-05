package sched

// adr: 623
import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// The current admission gate denies every accepted quota head before issuance.
// Once independently activated, this boundary must issue receipts before RPC
// dispatch. It never releases them on an RPC error or terminal instance row.
func (e *Engine) prepareRegionalRuntimeLaunch(ctx context.Context, node, instance, run, purpose string, spec AppSpec) (AppSpec, error) {
	if err := checkRegionalRuntimeQuota(ctx, e.store, spec.AppID); err != nil {
		return AppSpec{}, err
	}
	appID, err := regionalLaunchID(spec.AppID)
	if err != nil {
		return AppSpec{}, err
	}
	head, err := e.store.RegionalRuntimeQuotaHead(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		if spec.RegionalRuntime != nil {
			return AppSpec{}, runtimequota.ErrTrust
		}
		return spec, ctx.Err()
	}
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: regional launch head: %w", err)
	}
	// A head may arrive between the two reads. Do not reserve against authority
	// that did not pass the admission gate; current heads are all frozen.
	if err := checkRegionalRuntimeQuota(ctx, e.store, spec.AppID); err != nil {
		return AppSpec{}, err
	}
	spec, err = e.issueRegionalRuntimeLaunch(ctx, node, instance, run, purpose, spec, head.Grant)
	if err != nil {
		return spec, err // Keep the original attempt when an issuance reply is uncertain.
	}
	return spec, checkRegionalRuntimeQuota(ctx, e.store, spec.AppID)
}

// Issuance is independently testable bookkeeping. It does not admit work.
func (e *Engine) issueRegionalRuntimeLaunch(ctx context.Context, node, instance, run, purpose string, spec AppSpec, grant runtimequota.Grant) (AppSpec, error) {
	actualInstanceKey := instance
	if (purpose == runtimequota.CapacityPrime) != spec.PrimeOnly {
		return AppSpec{}, runtimequota.ErrBinding
	}
	ids := []*string{&spec.AppID, &spec.AccountID, &spec.DeploymentID, &instance, &run, &node}
	for _, id := range ids {
		value, err := regionalLaunchID(*id)
		if err != nil {
			return AppSpec{}, err
		}
		*id = value
	}
	if grant.Validate() != nil || grant.Binding.RegionalAppID != spec.AppID || grant.Binding.RegionalAccountID != spec.AccountID || grant.Policy.Plan != spec.Plan {
		return AppSpec{}, runtimequota.ErrBinding
	}
	registered, err := e.store.ComputeNodeByID(ctx, node)
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: regional launch node: %w", err)
	}
	if registered.ID != node || !registered.Active {
		return AppSpec{}, runtimequota.ErrBinding
	}
	stats, err := e.vmm.Stats(ctx, registered.ID)
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: regional launch epoch: %w", err)
	}
	epoch, err := stats.RegionalRuntimeEpochForNode(registered.ID)
	if err != nil {
		return AppSpec{}, err
	}
	binding := runtimequota.LifecycleBinding{InstanceID: instance, AppID: spec.AppID, AccountID: spec.AccountID, DeploymentID: spec.DeploymentID, Plan: spec.Plan, Purpose: purpose}
	placement, err := runtimequota.NewLaunchPlacement(grant, epoch, binding, run, false)
	if err != nil {
		return AppSpec{}, err
	}
	if spec.RegionalRuntime != nil && (spec.RegionalRuntime.Permit != placement.Permit || !sameInitialServingSeat(spec.RegionalRuntime.Seat, placement.Seat)) {
		return AppSpec{}, runtimequota.ErrBinding
	}
	source := runtimequota.LaunchSource{Permit: placement.Permit, InstanceKey: actualInstanceKey}
	if source.Validate() != nil {
		return AppSpec{}, runtimequota.ErrBinding
	}
	resources, valid := regionalRuntimeLaunchResources(spec)
	if !valid {
		return spec, runtimequota.ErrBinding // A retry must retain any already escaped original placement.
	}
	spec.RegionalRuntime = &placement
	if err := e.ledger.bindOriginalRegionalRuntime(source); err != nil {
		return spec, err
	}
	if err := e.store.IssueRegionalRuntimeLaunchResources(ctx, placement, source, resources); err != nil {
		return spec, fmt.Errorf("sched: issue regional launch: %w", err)
	}
	if err := e.store.CheckRegionalRuntimeIssuedPlacement(ctx, placement); err != nil {
		return spec, fmt.Errorf("sched: check issued regional launch: %w", err)
	}
	return spec, nil
}

// adr: 637
// Match the guest RAM and conservative CPU peak vmmd will actually launch.
// VM overhead is charged separately by the ledger, once per physical runtime.
func regionalRuntimeLaunchResources(spec AppSpec) (state.RegionalRuntimeResources, bool) {
	sidecarRAM := make([]int, len(spec.Sidecars))
	for i, sidecar := range spec.Sidecars {
		sidecarRAM[i] = sidecar.RamMB
	}
	resources := state.RegionalRuntimeResources{RAMMB: api.BillableRAMMBWithSidecars(int(spec.MemSizeMiB), sidecarRAM) - api.PerVMOverheadMB, VCPU: int(spec.VCPUCount), CPUMillicores: int(spec.CPUMillicores)}
	period, quota := spec.Plan.CPUPeriodUS(), spec.Plan.CPUQuotaUS()
	if spec.MemSizeMiB <= 0 || period <= 0 || quota <= 0 {
		return resources, false
	}
	ceiling := int(int64(quota) * 1000 / int64(period))
	if resources.CPUMillicores != 0 && !api.ValidAppCPUMillicores(resources.CPUMillicores) {
		return resources, false
	}
	if resources.CPUMillicores == 0 {
		resources.CPUMillicores = ceiling
	}
	if resources.CPUMillicores > ceiling {
		return resources, false
	}
	if !spec.DisableStartupCPUBoost {
		resources.CPUMillicores = startupCPUBoostQuota(spec.Plan, resources.CPUMillicores)
	}
	return resources, resources.Valid()
}

func regionalLaunchID(id string) (string, error) {
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil {
		return "", runtimequota.ErrInvalid
	}
	return u.String(), nil
}

func sameInitialServingSeat(a, b *runtimequota.ServingSeat) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
