package reconcile

import (
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

const EmptyWorkloadPlanReason = "scan produced zero workloads; reconcile refused"

var ErrInvalidWorkloadPlan = errors.New("reconcile: invalid workload plan")

// WorkloadAdmissionReasons applies the create-time app identity constraints
// before a project plan is declared applicable. A same-key app is an intended
// project member update; reusing an account-wide slug with another key would
// fail the later create and can otherwise leave a partially applied project.
func WorkloadAdmissionReasons(workloads []reposcan.Workload, accountApps []state.App, projectID string) []string {
	if len(workloads) == 0 {
		return []string{EmptyWorkloadPlanReason}
	}

	bySlug := make(map[string]state.App, len(accountApps))
	for _, app := range accountApps {
		bySlug[app.Slug] = app
	}
	seen := make(map[string]struct{}, len(workloads))
	var reasons []string
	for _, workload := range workloads {
		if !api.ValidAppSlug(workload.Name) {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q has invalid app slug %q; use 3-40 lowercase letters, digits, or hyphens",
				workload.Name, workload.Name))
			continue
		}
		if _, duplicate := seen[workload.Name]; duplicate {
			reasons = append(reasons, fmt.Sprintf("workload %q produces a duplicate app slug", workload.Name))
			continue
		}
		seen[workload.Name] = struct{}{}
		if app, exists := bySlug[workload.Name]; exists &&
			(projectID == "" || app.ProjectID != projectID ||
				app.RootDir != workload.RootDir || app.WorkloadName != workload.Name) {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q conflicts with existing app slug %q outside this project member",
				workload.Name, app.Slug))
		}
	}
	return reasons
}

func validateWorkloadAdmission(workloads []reposcan.Workload, accountApps []state.App, projectID string) error {
	reasons := WorkloadAdmissionReasons(workloads, accountApps, projectID)
	if len(reasons) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidWorkloadPlan, strings.Join(reasons, "; "))
}
