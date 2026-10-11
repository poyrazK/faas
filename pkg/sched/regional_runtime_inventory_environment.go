package sched

// adr: 792
import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// Attribution belongs to an original physical hold, independently of mutable
// instance rows and of later deployments in an environment with the same slug.
type regionalInventoryEnvironment struct {
	key        string
	production bool
}

func regionalInventoryEnvironmentError(entry state.RegionalRuntimeInventoryEntry, owner *regionalInventoryEnvironment) error {
	if entry.ServingSeatsHeld > 0 && owner == nil {
		return fmt.Errorf("sched: original serving environment missing: %w", ErrRegionalRuntimeRecoveryUnresolved)
	}
	return nil
}

func (e *Engine) regionalInventoryEnvironment(ctx context.Context, entry state.RegionalRuntimeInventoryEntry) (*regionalInventoryEnvironment, error) {
	if entry.ServingSeatsHeld == 0 {
		return nil, nil
	}
	p := entry.Receipt.Permit
	if p.Validate() != nil {
		return nil, runtimequota.ErrBinding
	}
	app, err := e.store.AppByID(ctx, p.Grant.Binding.RegionalAppID)
	if err != nil || app.Status == state.AppDeleted || !runtimequota.MatchesID(p.Grant.Binding.RegionalAppID, app.ID) ||
		!runtimequota.MatchesID(p.Grant.Binding.RegionalAccountID, app.AccountID) {
		return nil, errors.Join(ErrRegionalRuntimeRecoveryUnresolved, err)
	}
	deployment, err := e.store.DeploymentByID(ctx, p.DeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		// Historical MemStore rows use compact UUIDs. This is the same ID,
		// never a slug lookup or a replacement deployment.
		deployment, err = e.store.DeploymentByID(ctx, strings.ReplaceAll(p.DeploymentID, "-", ""))
	}
	if err != nil || !runtimequota.MatchesID(p.DeploymentID, deployment.ID) || !runtimequota.MatchesID(p.Grant.Binding.RegionalAppID, deployment.AppID) {
		return nil, errors.Join(ErrRegionalRuntimeRecoveryUnresolved, err)
	}
	owner, err := e.store.RuntimeScalingStateForDeployment(ctx, app.AccountID, app.ID, deployment.ID)
	scope := deployment.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	validEnvironment := true
	if owner.EnvironmentID != "" {
		id, parseErr := uuid.Parse(owner.EnvironmentID)
		validEnvironment = parseErr == nil && id != uuid.Nil
	}
	if err != nil || !runtimequota.MatchesID(p.Grant.Binding.RegionalAccountID, owner.AccountID) ||
		!runtimequota.MatchesID(p.Grant.Binding.RegionalAppID, owner.AppID) || !runtimequota.MatchesID(p.DeploymentID, owner.DeploymentID) ||
		api.ValidateScope(owner.Scope) != nil || owner.Scope != scope || !validEnvironment {
		return nil, errors.Join(ErrRegionalRuntimeRecoveryUnresolved, err)
	}
	return &regionalInventoryEnvironment{key: runtimeEnvironmentAdmissionKey(owner.Scope, owner.EnvironmentID), production: reaperProductionScope(owner.Scope)}, nil
}
