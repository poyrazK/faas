package sched

// adr: 627
import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrRegionalRuntimeRecoveryUnresolved blocks startup with incomplete serving
// accounting. Ordinary recovery errors retain their existing startup behavior.
var ErrRegionalRuntimeRecoveryUnresolved = errors.New("regional runtime recovery is unresolved")

// prepareRegionalWarmResume is bookkeeping, independently of admission. The
// production caller must pass the quota gate before and after this operation.
// An uncertain issuance retains the provisional local concurrency reservation.
func (e *Engine) prepareRegionalWarmResume(ctx context.Context, app state.App, acct state.Account, dep state.Deployment, warm state.Instance, grant runtimequota.Grant) (runtimequota.Placement, error) {
	if warm.State != string(state.StateWarm) || warm.AppID != app.ID || warm.DeploymentID != dep.ID || dep.AppID != app.ID || app.AccountID != acct.ID || warm.Netns == "" || warm.HostIP == "" {
		return runtimequota.Placement{}, runtimequota.ErrBinding
	}
	appID, acctID, depID, instanceID, nodeID, runID := app.ID, acct.ID, dep.ID, warm.ID, warm.NodeID, warm.WakeID
	for _, id := range []*string{&appID, &acctID, &depID, &instanceID, &nodeID, &runID} {
		canonical, err := regionalLaunchID(*id)
		if err != nil {
			return runtimequota.Placement{}, runtimequota.ErrBinding
		}
		*id = canonical
	}
	if grant.Validate() != nil || grant.Binding.RegionalAppID != appID || grant.Binding.RegionalAccountID != acctID || ((app.OrgID == "") != (grant.Binding.RegionalOrgID == "")) || (app.OrgID != "" && !runtimequota.MatchesID(grant.Binding.RegionalOrgID, app.OrgID)) || grant.Policy.Plan != acct.Plan {
		return runtimequota.Placement{}, runtimequota.ErrBinding
	}
	node, err := e.store.ComputeNodeByID(ctx, warm.NodeID)
	if err != nil {
		return runtimequota.Placement{}, fmt.Errorf("sched: regional warm node: %w", err)
	}
	if !runtimequota.MatchesID(nodeID, node.ID) || !node.Active || stringValue(node.Region) != grant.Binding.Region {
		return runtimequota.Placement{}, runtimequota.ErrBinding
	}
	stats, err := e.vmm.Stats(ctx, node.ID)
	if err != nil {
		return runtimequota.Placement{}, fmt.Errorf("sched: regional warm epoch: %w", err)
	}
	epoch, err := stats.RegionalRuntimeEpochForNode(nodeID)
	if err != nil {
		return runtimequota.Placement{}, err
	}
	p, err := runtimequota.NewLaunchPlacement(grant, epoch, runtimequota.LifecycleBinding{InstanceID: instanceID, AppID: appID, AccountID: acctID, DeploymentID: depID, Plan: acct.Plan, Purpose: runtimequota.CapacityWarm}, runID, false)
	if err != nil {
		return runtimequota.Placement{}, err
	}
	physical, err := e.store.RegionalRuntimeCapacity(ctx, appID, p.Permit.ID)
	if err != nil {
		return runtimequota.Placement{}, fmt.Errorf("sched: regional warm physical receipt: %w", err)
	}
	if physical.Permit != p.Permit {
		return runtimequota.Placement{}, runtimequota.ErrBinding
	}
	if err := e.store.CheckRegionalRuntimeIssuedPlacement(ctx, p); err != nil {
		return runtimequota.Placement{}, err
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return runtimequota.Placement{}, runtimequota.ErrBinding
	}
	if err := e.ledger.reserveRegionalWarmResume(warm.ID, p.Permit, effectiveMaxConcurrency(app, limits), false); err != nil {
		return runtimequota.Placement{}, err
	}
	proposal, err := runtimequota.NewServingResumeIntent(p.Permit, uuid.NewString(), false)
	if err != nil {
		return runtimequota.Placement{}, err
	}
	retained, err := e.store.IssueRegionalRuntimeServingResume(ctx, proposal)
	if err != nil {
		return runtimequota.Placement{}, fmt.Errorf("sched: issue regional warm resume: %w", err)
	}
	if retained.Intent.Validate() != nil || retained.Intent.Seat.Permit != p.Permit || retained.Intent.Seat.Rollout {
		return runtimequota.Placement{}, runtimequota.ErrBinding
	}
	placement := retained.Intent.Placement()
	return placement, e.store.CheckRegionalRuntimeIssuedPlacement(ctx, placement)
}

// dispatchRegionalWarmResume only handles the transport step. Its caller must
// independently admit work. Receipt checks and an exact transport ack never
// authorize a release or stand in for native lifecycle acceptance.
func (e *Engine) dispatchRegionalWarmResume(ctx context.Context, warm state.Instance, p runtimequota.Placement) error {
	if !regionalWarmPlacementMatches(warm, p) {
		return runtimequota.ErrBinding
	}
	resumer, ok := e.vmm.(RegionalWarmResumeVMM)
	if !ok {
		return regionalWarmUnavailable(runtimequota.ErrTrust)
	}
	if err := e.store.CheckRegionalRuntimeIssuedPlacement(ctx, p); err != nil {
		return err
	}
	if err := resumer.ResumeRegionalWarmInstance(ctx, warm.NodeID, warm.ID, p.Clone()); err != nil {
		return err
	}
	return e.store.CheckRegionalRuntimeIssuedPlacement(ctx, p)
}

func regionalWarmPlacementMatches(warm state.Instance, p runtimequota.Placement) bool {
	return p.Validate() == nil && p.Seat != nil && p.Permit.Kind == runtimequota.CapacityWarm && runtimequota.MatchesID(p.Permit.InstanceID, warm.ID) && runtimequota.MatchesID(p.Permit.NodeID, warm.NodeID) && runtimequota.MatchesID(p.Permit.RunID, warm.WakeID) && runtimequota.MatchesID(p.Permit.DeploymentID, warm.DeploymentID) && runtimequota.MatchesID(p.Permit.Grant.Binding.RegionalAppID, warm.AppID)
}

func regionalWarmUnavailable(err error) error {
	return errors.Join(err, api.NewProblem(503, api.CodeCapacity, "Regional warm resume unavailable", "the resident resume attempt must be reconciled before serving"))
}

// A WARM row may already have escaped as a serving resume. Read held seats
// without admission, current-grant or host-epoch checks: fences and retired
// grants still count until authenticated release. Ambiguity blocks startup.
func (e *Engine) regionalWarmRecovery(ctx context.Context, app state.App, acct state.Account, warm state.Instance) (runtimequota.CapacityPermit, bool, error) {
	appID, err := regionalLaunchID(app.ID)
	if err != nil {
		return runtimequota.CapacityPermit{}, true, err
	}
	_, err = e.store.RegionalRuntimeQuotaHead(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		return runtimequota.CapacityPermit{}, false, nil
	}
	if err != nil {
		return runtimequota.CapacityPermit{}, true, err
	}
	instanceID, nodeID, runID := warm.ID, warm.NodeID, warm.WakeID
	for _, id := range []*string{&instanceID, &nodeID, &runID} {
		canonical, err := regionalLaunchID(*id)
		if err != nil {
			return runtimequota.CapacityPermit{}, true, err
		}
		*id = canonical
	}
	r, err := e.store.HeldRegionalRuntimeServingResumeForInstance(ctx, appID, instanceID, nodeID, runID)
	if errors.Is(err, state.ErrNotFound) {
		return runtimequota.CapacityPermit{}, false, nil
	}
	if err != nil {
		return runtimequota.CapacityPermit{}, true, err
	}
	p := r.Intent.Seat.Permit
	if r.Intent.Validate() != nil || p.Kind != runtimequota.CapacityWarm || p.InstanceID != instanceID || p.NodeID != nodeID || p.RunID != runID || !runtimequota.MatchesID(p.DeploymentID, warm.DeploymentID) || p.Grant.Binding.RegionalAppID != appID || !runtimequota.MatchesID(p.Grant.Binding.RegionalAccountID, acct.ID) || app.AccountID != acct.ID {
		return runtimequota.CapacityPermit{}, true, runtimequota.ErrBinding
	}
	return p, true, nil
}
