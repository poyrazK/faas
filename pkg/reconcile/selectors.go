package reconcile

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func WorkloadMatchesSelectors(selectors map[string]bool, workload reposcan.Workload) bool {
	return selectors[strings.ToLower(workload.Name)] ||
		(workload.AppSlug != "" && selectors[strings.ToLower(workload.AppSlug)]) ||
		(workload.RootDir != "" && selectors[strings.ToLower(workload.RootDir)])
}

func AppMatchesSelectors(selectors map[string]bool, app state.App) bool {
	return selectors[strings.ToLower(app.Slug)] || selectors[strings.ToLower(app.WorkloadName)] ||
		(app.RootDir != "" && selectors[strings.ToLower(app.RootDir)])
}
