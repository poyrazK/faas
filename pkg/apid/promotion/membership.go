package promotion

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/state"
)

// Membership includes both environments so a target-only service still blocks
// accidental removal. Services confined to other stages cannot expand a plan.
// Fresh plans and durable resumes must use the same membership rule.
func Membership(ctx context.Context, store any, accountID, projectID, from, to string, apps []state.App) ([]state.App, error) {
	selected := make(map[string]bool)
	for _, environment := range []string{from, to} {
		members, err := state.ProjectAppsForEnvironment(ctx, store, accountID, projectID, environment, apps)
		if err != nil {
			return nil, err
		}
		for _, app := range members {
			selected[app.ID] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(apps), func(app state.App) bool { return !selected[app.ID] }), nil
}
