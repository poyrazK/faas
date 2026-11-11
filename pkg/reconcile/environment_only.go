package reconcile

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/state"
)

func preserveEnvironmentOnlyActions(ctx context.Context, store state.Store, project state.Project, actions []Action) ([]Action, error) {
	preserved, err := state.EnvironmentOnlyProjectApps(ctx, store, project.AccountID, project.ID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(actions, func(action Action) bool { return action.Op == "remove" && preserved[action.App.ID] }), nil
}
