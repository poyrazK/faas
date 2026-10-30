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

// keepServingClass keeps an existing app's serving class when the scan could
// not read the service's prebuilt image (ADR-20261029120000 amendment 2). A
// registry that is down or a private image must not turn a live internal API
// into a worker: worker mode refuses the wakes its callers need. A new app
// has nothing to keep and starts as a worker, which the plan warns about.
func keepServingClass(w reposcan.Workload, app state.App) reposcan.Workload {
	if !w.ListenerUnknown || w.Class != reposcan.ClassWorker {
		return w
	}
	switch app.WorkloadClass {
	case state.WorkloadClassWorker, state.WorkloadClassJob:
		return w
	}
	w.Class = reposcan.ClassHTTP
	return w
}
