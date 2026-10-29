package reconcile

import (
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// RenamedWorkloads maps each service a scan renames (Compose
// x-gregale-renamed-from) from its previous name to its new one
// (ADR-20261029090000). existing is the project's production apps. A hint
// applies only when the previous name has an app and the new name has none:
// a first deploy has nothing to keep, and a new name that already has an app
// keeps that app while the previous one is removed as before.
func RenamedWorkloads(workloads []reposcan.Workload, existing []state.App) map[string]string {
	byName := make(map[string]bool, len(existing))
	for _, app := range existing {
		if app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			byName[app.WorkloadName] = true
		}
	}
	renamed := make(map[string]string)
	for _, workload := range workloads {
		if workload.RenamedFrom == "" || byName[workload.Name] || !byName[workload.RenamedFrom] {
			continue
		}
		renamed[workload.RenamedFrom] = workload.Name
	}
	return renamed
}
