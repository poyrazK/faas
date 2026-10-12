package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// SlugLookup resolves the app row holding a slug, including a deleted app
// still in its grace window (state.Store.AppBySlugIncludingDeleted).
type SlugLookup func(ctx context.Context, slug string) (state.App, error)

// SlugHolders returns, by slug, the apps outside accountApps that already hold
// a workload's app slug. apps.slug is unique across every account and a
// deleted app keeps its slug until the grace window ends, so admission against
// the account's live apps alone reported can_apply while the create was
// certain to fail (hunt #9: 409 workload_slug_conflict, "state: conflict:
// apps_slug_key").
func SlugHolders(ctx context.Context, lookup SlugLookup, workloads []reposcan.Workload, accountApps []state.App) (map[string]state.App, error) {
	known := make(map[string]bool, len(accountApps))
	for _, app := range accountApps {
		known[app.Slug] = true
	}
	holders := map[string]state.App{}
	for _, workload := range workloads {
		if known[workload.Name] || !api.ValidAppSlug(workload.Name) {
			continue
		}
		known[workload.Name] = true
		app, err := lookup(ctx, workload.Name)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reconcile: look up app slug %q: %w", workload.Name, err)
		}
		holders[workload.Name] = app
	}
	return holders, nil
}

// SlugHolderReasons explains each workload whose slug SlugHolders found held.
func SlugHolderReasons(accountID string, workloads []reposcan.Workload, holders map[string]state.App) []string {
	var reasons []string
	for _, workload := range workloads {
		app, held := holders[workload.Name]
		switch {
		case !held:
		case app.AccountID == accountID && app.Status == state.AppDeleted:
			reasons = append(reasons, fmt.Sprintf(
				"workload %q uses app slug %q, held by a deleted app until its grace window ends; restore it with `gregale apps restore %s` or rename the workload",
				workload.Name, workload.Name, workload.Name))
		default:
			reasons = append(reasons, fmt.Sprintf(
				"workload %q uses app slug %q, which is already taken; rename the workload",
				workload.Name, workload.Name))
		}
	}
	return reasons
}
