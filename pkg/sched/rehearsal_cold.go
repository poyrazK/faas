package sched

import (
	"context"
	"errors"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

type coldRetirementVMM interface {
	RetireRehearsalCold(context.Context, string, string) error
}
type routedColdRetirementVMM interface {
	RetireRehearsalColdOnNode(context.Context, string, string, string) error
}

func (c *VMMClient) RetireRehearsalCold(ctx context.Context, instance, prep string) error {
	r, err := c.cli.Destroy(ctx, &vmmdpb.DestroyRequest{Instance: instance, RehearsalColdPreparationId: prep})
	if err != nil {
		return err
	}
	if r.GetInstance() != instance || r.GetRehearsalColdPreparationId() != prep {
		return errors.New("cold-start retirement RPC did not acknowledge preparation identity")
	}
	return nil
}
func (r *VMMRouter) RetireRehearsalColdOnNode(ctx context.Context, node, instance, prep string) error {
	cli, err := r.resolveFor(ctx, node)
	if err != nil {
		return err
	}
	vmm, ok := cli.(coldRetirementVMM)
	if !ok {
		return errors.New("cold-start VM retirement capability unavailable")
	}
	return vmm.RetireRehearsalCold(ctx, instance, prep)
}

// parkScenarioCold runs under the existing app lock and parked-app guard.
// A lost reply can retire a row from its durable receipt, never repeat a kill.
func (e *Engine) parkScenarioCold(ctx context.Context, app string) (bool, int, error) {
	store, ok := e.store.(rehearsalcold.Store)
	if !ok {
		return false, 0, nil
	}
	p, err := store.ScenarioColdPreparation(ctx, app)
	if errors.Is(err, state.ErrNotFound) {
		return false, 0, nil
	}
	if err != nil {
		return true, 0, err
	}
	if p.State == "ready" || p.State == "booted" {
		return false, 0, nil
	}
	if p.InstanceID == "" && p.State == "retired" {
		return true, 0, nil
	}
	if p.State != "armed" && p.State != "retired" {
		_ = store.FailScenarioColdPreparation(ctx, app)
		return true, 0, errors.New("cold-start retirement owner or receipt was lost")
	}
	ins, err := e.store.InstanceByID(ctx, p.InstanceID)
	if err != nil {
		return true, 0, err
	}
	if ins.AppID != p.AppID || ins.DeploymentID != p.DeploymentID {
		return true, 0, state.ErrConflict
	}
	if p.State == "armed" {
		p, err = store.ClaimScenarioColdPreparation(ctx, app)
		if err != nil {
			return true, 0, err
		}
		vmm, supported := e.vmm.(routedColdRetirementVMM)
		if !supported || ins.State != "running" {
			_ = store.FailScenarioColdPreparation(ctx, app)
			return true, 0, errors.New("cold-start original VM owner unavailable")
		}
		operation, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = vmm.RetireRehearsalColdOnNode(operation, ins.NodeID, ins.ID, p.ID)
		cancel()
		receipt, readErr := store.ScenarioColdPreparation(ctx, app)
		if readErr != nil || receipt.State != "retired" {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer done()
			_ = store.FailScenarioColdPreparation(cleanup, app)
			return true, 0, errors.Join(err, readErr, errors.New("cold-start retirement receipt is missing"))
		}
	}
	if coldSchedTerminal(ins.State) {
		return true, 0, nil
	}
	changed, err := e.transitionWithKindCAS(ctx, ins.ID, ins.AppID, state.StateStopped, "rehearsal_cold_preparation", p.ID)
	if err != nil {
		return true, 0, err
	}
	if changed {
		e.ledger.Release(ins.ID)
		return true, 1, nil
	}
	return true, 0, nil
}
func coldSchedTerminal(s string) bool { return s == "stopped" || s == "parked" || s == "failed" }

func eligibleColdPrime(app state.App, member state.ScenarioTestMember, dep state.Deployment) bool {
	return app.AccountID == member.AccountID && app.ID == member.AppID && dep.AppID == app.ID && app.PreviewOfSlug != "" && app.PreviewPrNumber == 0 && app.PreviewPrState == state.PreviewPrStateOpen && app.PreviewExpiresAt != nil && app.PreviewExpiresAt.After(time.Now()) && member.NetworkMode == "contained" && member.SimulationJSON != ""
}
