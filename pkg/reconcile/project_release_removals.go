package reconcile

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ProjectSourceRemovals plans an atomic release that retires serving services
// the source no longer declares (ADR-20261021120000). The remaining services
// must keep their settings; nothing may still depend on a retired one, which
// the unchanged-configuration check enforces because their dependency lists
// cannot change. Only HTTP services can be retired in this version.
func ProjectSourceRemovals(serving []state.App, scan reposcan.Result) ([]state.App, []api.ProjectReleaseRemoval, error) {
	declared := map[string]bool{}
	for _, w := range scan.Workloads {
		declared[w.RootDir+"\x00"+w.Name] = true
	}
	var remaining []state.App
	var removals []api.ProjectReleaseRemoval
	for _, app := range serving {
		if app.PreviewOfSlug != "" || app.Status == state.AppDeleted {
			continue
		}
		if declared[app.RootDir+"\x00"+app.WorkloadName] {
			remaining = append(remaining, app)
			continue
		}
		if app.WorkloadClass != state.WorkloadClassHTTP || app.Manifest.ExecutionMode == api.ExecutionModeWorker || app.Manifest.ExecutionMode == api.ExecutionModeService {
			return nil, nil, fmt.Errorf("service %s cannot be retired atomically: only HTTP services can be removed", app.WorkloadName)
		}
		removals = append(removals, api.ProjectReleaseRemoval{AppID: app.ID, Slug: app.Slug, RootDir: app.RootDir, Fingerprint: state.ProjectReleaseAdditionFingerprint(app)})
	}
	if len(removals) == 0 {
		return nil, nil, fmt.Errorf("--remove-services requires a serving service that the source no longer declares")
	}
	if len(remaining) == 0 {
		return nil, nil, fmt.Errorf("an atomic release must keep at least one serving service")
	}
	selected, err := ProjectSafeSourceApps(remaining, scan)
	if err != nil {
		return nil, nil, err
	}
	return selected, removals, nil
}
