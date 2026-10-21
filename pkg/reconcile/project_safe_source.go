package reconcile

import (
	"fmt"
	"strings"

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

	if len(existing) == 0 || len(existing) != len(scan.Workloads) {
		return nil, fmt.Errorf("safe deploy requires unchanged project membership")
	}
	// Backing services declared beside the workloads (scan.Managed) are never
	// provisioned or configured by apply, so they cannot change the release.
	// Dependencies on them must still form a valid graph (ADR-20261021110000).
	if reasons := reposcan.DependencyValidationReasons(scan.Workloads, scan.Managed, scan.DependencyReferences); len(reasons) > 0 {
		return nil, fmt.Errorf("safe deploy requires a valid dependency graph: %s", strings.Join(reasons, "; "))
	}
	byKey := map[string]state.App{}
	for _, app := range existing {
		byKey[app.RootDir+"\x00"+app.WorkloadName] = app
	}
	var apps []state.App
	for _, w := range scan.Workloads {
		app, found := byKey[w.RootDir+"\x00"+w.Name]
		worker := len(migration) > 1 && migration[1] && app.Manifest.ExecutionMode == api.ExecutionModeWorker
		// An image workload stays an image workload: its reference is the
		// release's source, every other image setting must be unchanged
		// (ADR-20261021113000).
		image := found && w.Image != "" && app.Manifest.ProjectImage != ""
		if !found || !worker && (app.WorkloadClass != state.WorkloadClassHTTP || app.Manifest.ExecutionMode == api.ExecutionModeService || app.Manifest.ExecutionMode == api.ExecutionModeWorker) || w.Image != "" && !image || len(w.ReleaseCommand) > 0 && (len(migration) == 0 || !migration[0]) {
			return nil, fmt.Errorf("safe deploy supports existing HTTP source or image workloads without release commands")
		}
		for _, field := range diffFieldsChanged(app, w, resolveStartCommand(w), workloadNameSet(scan.Workloads)) {
			if field != "source" && (field != "image" || !image) {
				return nil, fmt.Errorf("safe deploy cannot change workload configuration (%s)", field)
			}
		}
		if image {
			// The candidate runs the scanned reference. The serving app's
			// record is not reconciled, like a source workload's checkpoint.
			app.Manifest.ProjectImage = w.Image
		}
		delete(byKey, w.RootDir+"\x00"+w.Name)
		apps = append(apps, app)
	}
	return apps, nil
}
