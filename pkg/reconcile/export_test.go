// export_test.go exposes unexported identifiers to the external test package
// reconcile_test.

package reconcile

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// DraftAppForScan is the draft Reconcile creates for w in a project with no
// apps yet.
func DraftAppForScan(project state.Project, scan reposcan.Result, w reposcan.Workload, plan api.Plan) state.App {
	return workloadToDraftApp(project, w, resolveStartCommand(w), plan, availableServiceNames(scan, project, nil))
}
