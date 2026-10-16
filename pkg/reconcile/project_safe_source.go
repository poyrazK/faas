package reconcile

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ProjectSafeSourceApps is read-only. Safe source applies bypass reconciliation
// so a candidate failure cannot modify the serving app configuration.
func ProjectSafeSourceApps(existing []state.App, scan reposcan.Result, migration ...bool) ([]state.App, error) {
	production := make([]state.App, 0, len(existing))
	for _, app := range existing {
		if app.PreviewOfSlug == "" {
			production = append(production, app)
		}
	}
	existing = production

	if len(existing) == 0 || len(existing) != len(scan.Workloads) || len(scan.Managed) > 0 {
		return nil, fmt.Errorf("safe deploy requires unchanged project membership without managed resources")
	}
	byKey := map[string]state.App{}
	for _, app := range existing {
		byKey[app.RootDir+"\x00"+app.WorkloadName] = app
	}
	var apps []state.App
	for _, w := range scan.Workloads {
		app, found := byKey[w.RootDir+"\x00"+w.Name]
		worker := len(migration) > 1 && migration[1] && app.Manifest.ExecutionMode == api.ExecutionModeWorker
		if !found || !worker && (app.WorkloadClass != state.WorkloadClassHTTP || app.Manifest.ExecutionMode == api.ExecutionModeService || app.Manifest.ExecutionMode == api.ExecutionModeWorker) || w.Image != "" || len(w.ReleaseCommand) > 0 && (len(migration) == 0 || !migration[0]) {
			return nil, fmt.Errorf("safe deploy supports existing HTTP source workloads without release commands")
		}
		for _, field := range diffFieldsChanged(app, w, resolveStartCommand(w), workloadNameSet(scan.Workloads)) {
			if field != "source" {
				return nil, fmt.Errorf("safe deploy cannot change workload configuration (%s)", field)
			}
		}
		delete(byKey, w.RootDir+"\x00"+w.Name)
		apps = append(apps, app)
	}
	return apps, nil
}
