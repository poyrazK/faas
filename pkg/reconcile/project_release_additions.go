package reconcile

import (
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ProjectSourceAdditions plans without creating apps. Dormant reservations do
// not form part of serving membership; an identical reservation can be reused.
func ProjectSourceAdditions(project state.Project, plan api.Plan, ttl int, existing []state.App, dormant map[string]bool, scan reposcan.Result) ([]state.App, []state.App, []api.ProjectReleaseAddition, error) {
	var serving, selected, drafts []state.App
	byKey := map[string]state.App{}
	servingNames := map[string]bool{}
	for _, app := range existing {
		if app.PreviewOfSlug != "" || app.Status == state.AppDeleted {
			continue
		}
		byKey[app.RootDir+"\x00"+app.WorkloadName] = app
		if !dormant[app.ID] {
			serving = append(serving, app)
			servingNames[app.WorkloadName] = true
		}
	}
	unchanged := reposcan.Result{Managed: scan.Managed}
	var additions []api.ProjectReleaseAddition
	for _, w := range scan.Workloads {
		app, found := byKey[w.RootDir+"\x00"+w.Name]
		if found && !dormant[app.ID] {
			for _, target := range w.DependsOn {
				if !servingNames[target] {
					return nil, nil, nil, fmt.Errorf("existing service %s cannot add a dependency during an HTTP service addition", w.Name)
				}
			}
			for target := range w.ServiceReliability {
				if !servingNames[target] {
					return nil, nil, nil, fmt.Errorf("existing service %s cannot add service policy during an HTTP service addition", w.Name)
				}
			}
			unchanged.Workloads = append(unchanged.Workloads, w)
			continue
		}
		// Additions deliberately exclude bindings, workers, schedules, images
		// and release commands. They cannot introduce a second traffic owner.
		if workloadClassFromScan(w) != state.WorkloadClassHTTP || w.Class != reposcan.ClassHTTP || w.Image != "" || len(w.ReleaseCommand) > 0 || len(w.DependsOn) > 0 || len(w.DependsOnConditions) > 0 || len(w.InternalPorts) > 0 || len(w.EnvKeys) > 0 || len(w.CronSchedules()) > 0 {
			return nil, nil, nil, fmt.Errorf("new atomic services must be independent HTTP source workloads without env, bindings, schedules, images or release commands")
		}
		draft := workloadToDraftApp(project, w, resolveStartCommand(w), plan, workloadNameSet(scan.Workloads))
		if draft.Manifest.ExecutionMode == api.ExecutionModeWorker || draft.Manifest.ExecutionMode == api.ExecutionModeService || len(draft.Manifest.ServiceBindings) > 0 {
			return nil, nil, nil, fmt.Errorf("new atomic services must use HTTP execution")
		}
		draft.Manifest.RevisionPinTTLSeconds = ttl
		if found {
			draft.ID = app.ID
			if state.ProjectReleaseAdditionFingerprint(app) != state.ProjectReleaseAdditionFingerprint(draft) {
				return nil, nil, nil, fmt.Errorf("reserved service %s settings changed; remove the unused reservation before retrying", w.Name)
			}
			draft = app
		}
		drafts = append(drafts, draft)
		additions = append(additions, api.ProjectReleaseAddition{AppID: draft.ID, Slug: draft.Slug, RootDir: draft.RootDir, Fingerprint: state.ProjectReleaseAdditionFingerprint(draft)})
	}
	var err error
	selected, err = ProjectSafeSourceApps(serving, unchanged)
	if err != nil {
		return nil, nil, nil, err
	}
	sort.Slice(additions, func(i, j int) bool { return additions[i].Slug < additions[j].Slug })
	return selected, drafts, additions, nil
}
