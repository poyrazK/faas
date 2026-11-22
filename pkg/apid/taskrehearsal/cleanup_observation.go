package taskrehearsal

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
)

// ObserveCleanup independently reads disposed resources for a completed run.
// Only the platform's deterministic build identities reach source storage.
func (p *Provisioner) ObserveCleanup(ctx context.Context, accountID, runID string,
	sourceExists func(context.Context, string) (bool, error)) (tasktoolrehearsal.CleanupObservation, error) {
	var observed tasktoolrehearsal.CleanupObservation
	if sourceExists == nil {
		return observed, state.ErrInvalidArgument
	}
	target, err := p.target(state.Account{ID: accountID}, runID)
	if err != nil {
		return observed, err
	}
	observed.DatabasePresent, observed.LoginsRemaining, err = p.Postgres.ObserveDisposedTaskFixture(ctx, accountID, target.scenarioID(), target.AppID(Migration))
	if err != nil {
		return observed, err
	}
	for _, role := range workloads {
		appID := target.AppID(role.name)
		app, err := p.Store.AppByID(ctx, appID)
		switch {
		case errors.Is(err, state.ErrNotFound):
		case err != nil:
			return observed, err
		case !target.owns(app, role.name):
			return observed, state.ErrConflict
		case app.Status != state.AppDeleted || app.PreviewPrState != state.PreviewPrStateTornDown:
			observed.WorkloadsRemaining++
		}
		secrets, err := p.Store.ListAppSecretsInScope(ctx, accountID, appID, api.DefaultEnvScope)
		if err != nil {
			return observed, err
		}
		observed.SecretsRemaining += len(secrets)
		if role.name != Observer {
			_, buildID := apidsource.OperationIDs(buildOperation(runID), appID)
			exists, err := sourceExists(ctx, buildID)
			if err != nil {
				return observed, err
			}
			if exists {
				observed.SourcesRemaining++
			}
		}
	}
	return observed, ctx.Err()
}
