package reconcile

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// AppSlug resolves an allocation name without changing repository identity.
func AppSlug(w reposcan.Workload) string {
	if w.AppSlug != "" {
		return w.AppSlug
	}
	return w.Name
}

// PreserveAppSlugs makes later scans and GitHub reconciliation retain the
// project's existing allocations. An explicit different slug is left in place
// so admission can reject a rename before any mutations.
func PreserveAppSlugs(workloads []reposcan.Workload, apps []state.App, projectID string) []reposcan.Workload {
	out := slices.Clone(workloads)
	for i := range out {
		if out[i].AppSlug != "" || projectID == "" {
			continue
		}
		for _, app := range apps {
			if app.ProjectID == projectID && app.WorkloadName == out[i].Name && app.Status != state.AppDeleted && app.PreviewOfSlug == "" {
				out[i].AppSlug = app.Slug
				break
			}
		}
	}
	return out
}
