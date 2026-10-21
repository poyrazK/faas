package serviceadditions

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// removalSurfaces reads what could still reach a retired service outside the
// release graph: schedules, queue deliveries and custom domains.
type removalSurfaces interface {
	ListCronsForApp(ctx context.Context, appID string) ([]state.Cron, error)
	ListQueueBindingsForApp(ctx context.Context, accountID, appID string) ([]state.QueueBinding, error)
	ListDomainsForApp(ctx context.Context, appID string) ([]state.CustomDomain, error)
}

// planRemovals retires serving services the source no longer declares
// (ADR-20261021120000). The baseline must be exactly the serving inventory.
func planRemovals(ctx context.Context, store any, account state.Account, project state.Project, safe *api.ProjectSafeRelease, apps []state.App, dormant map[string]bool, scan reposcan.Result) ([]state.App, error) {
	if _, ok := store.(state.ProjectReleaseRemovalsStore); !ok {
		return nil, fmt.Errorf("service retirements are unavailable")
	}
	surfaces, ok := store.(removalSurfaces)
	if !ok {
		return nil, fmt.Errorf("service retirement checks are unavailable")
	}
	graphs, ok := store.(state.ProjectReleaseSetReader)
	if !ok {
		return nil, fmt.Errorf("service retirement baseline is unavailable")
	}
	baseline, err := graphs.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
	if err != nil || baseline.ID != safe.PreviousReleaseID {
		return nil, fmt.Errorf("service retirement baseline changed")
	}
	var serving []state.App
	members := map[string]bool{}
	for _, member := range baseline.Members {
		members[member.AppID] = true
	}
	for _, app := range apps {
		if !dormant[app.ID] && app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			if !members[app.ID] {
				return nil, fmt.Errorf("project inventory differs from the serving baseline; reconcile it before removing services")
			}
			serving = append(serving, app)
		}
	}
	if len(serving) != len(baseline.Members) {
		return nil, fmt.Errorf("serving baseline membership changed")
	}
	selected, removals, err := reconcile.ProjectSourceRemovals(serving, scan)
	if err != nil {
		return nil, err
	}
	for _, removal := range removals {
		if err := checkRetirementSurfaces(ctx, surfaces, account.ID, removal); err != nil {
			return nil, err
		}
	}
	retired := map[string]bool{}
	for _, removal := range removals {
		retired[removal.AppID] = true
	}
	if safe.Verification != nil {
		// A retired service no longer answers; its probes would read as a regression.
		probes := safe.Verification.Probes[:0]
		for _, probe := range safe.Verification.Probes {
			if !retired[probe.AppID] {
				probes = append(probes, probe)
			}
		}
		if len(probes) == 0 {
			return nil, fmt.Errorf("--verify requires a route-health probe on a service that remains")
		}
		safe.Verification.Probes = probes
	}
	safe.Removals = removals
	safe.Deadline = time.Now().UTC().Add(api.ProjectSafeReleaseGracePeriod + time.Duration(len(selected)*api.BuildTimeoutSeconds)*time.Second)
	return selected, nil
}

func checkRetirementSurfaces(ctx context.Context, surfaces removalSurfaces, accountID string, removal api.ProjectReleaseRemoval) error {
	crons, err := surfaces.ListCronsForApp(ctx, removal.AppID)
	if err != nil {
		return fmt.Errorf("service %s schedules could not be read", removal.Slug)
	}
	for _, cron := range crons {
		if cron.Enabled {
			return fmt.Errorf("service %s has enabled schedules; disable them before retiring it", removal.Slug)
		}
	}
	bindings, err := surfaces.ListQueueBindingsForApp(ctx, accountID, removal.AppID)
	if err != nil {
		return fmt.Errorf("service %s queue bindings could not be read", removal.Slug)
	}
	for _, binding := range bindings {
		if binding.Enabled {
			return fmt.Errorf("service %s consumes a queue; remove the binding before retiring it", removal.Slug)
		}
	}
	domains, err := surfaces.ListDomainsForApp(ctx, removal.AppID)
	if err != nil {
		return fmt.Errorf("service %s domains could not be read", removal.Slug)
	}
	if len(domains) > 0 {
		return fmt.Errorf("service %s has custom domains; move them before retiring it", removal.Slug)
	}
	return nil
}

// StageRemovals records the retirements against the durable attempt.
func StageRemovals(ctx context.Context, store state.ProjectReleaseRemovalsStore, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) error {
	return store.StageProjectReleaseRemovals(ctx, accountID, attemptID, projectID, safe)
}

func AuditRetirements(ctx context.Context, auditor Auditor, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) {
	if auditor == nil {
		return
	}
	for _, removal := range safe.Removals {
		auditor.Emit(ctx, "project.workload.retirement_staged", &accountID, map[string]any{
			"project_id": projectID, "app_id": removal.AppID, "workload_name": removal.Slug,
			"root_dir": removal.RootDir, "attempt_id": attemptID, "baseline_release_id": safe.PreviousReleaseID,
		})
	}
}
