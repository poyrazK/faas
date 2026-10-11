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

func validatePlatformTenantPolicy(plan api.Plan, workloads []reposcan.Workload) error {
	if reasons := ProjectImageLifecycleAdmissionReasons(plan, workloads); len(reasons) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidWorkloadPlan, strings.Join(reasons, "; "))
	}
	if plan.ConsumerKeysPerApp() > 0 {
		return nil
	}
	for _, workload := range workloads {
		if workload.PlatformTenantRequired != nil && *workload.PlatformTenantRequired {
			return &api.APIError{Problem: *api.ErrPlanPlatformTenantRequiredNotAllowed(plan)}
		}
	}
	return nil
}

// ProjectImageLifecycleAdmissionReasons keeps background image workloads on
// their worker/job lifecycle and applies the same plan gate as standalone apps.
func ProjectImageLifecycleAdmissionReasons(plan api.Plan, workloads []reposcan.Workload) []string {
	var reasons []string
	for _, workload := range workloads {
		if workload.Image == "" {
			continue
		}
		manifest := api.AppManifest{ExecutionMode: projectImageExecutionMode(workload)}
		if err := manifest.ValidateLifecyclePlan(plan); err != nil {
			reasons = append(reasons, fmt.Sprintf("workload %q: %s", workload.Name, err))
		}
	}
	return reasons
}

// WorkloadAdmissionReasons applies the create-time app identity constraints
// before a project plan is declared applicable. A same-key app is an intended
// project member update; reusing an account-wide slug with another key would
// fail the later create and can otherwise leave a partially applied project.
func WorkloadAdmissionReasons(workloads []reposcan.Workload, accountApps []state.App, projectID string) []string {
	return WorkloadAdmissionReasonsWithManaged(workloads, nil, accountApps, projectID)
}

// WorkloadAdmissionReasonsWithManaged extends the project admission checks
// with the Compose dependency graph. Denylisted stateful images remain external
// managed resources and are not included in the deploy order.
func WorkloadAdmissionReasonsWithManaged(workloads []reposcan.Workload, managed []reposcan.Managed, accountApps []state.App, projectID string) []string {
	if len(workloads) == 0 {
		return []string{EmptyWorkloadPlanReason}
	}

	bySlug := make(map[string]state.App, len(accountApps))
	for _, app := range accountApps {
		bySlug[app.Slug] = app
	}
	seen := make(map[string]string, len(workloads))
	var reasons []string
	for _, workload := range workloads {
		if workload.DetectedBy.Detector == "serverless" {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q is a Serverless function without an execution adapter; create a function app and deploy the handler explicitly",
				workload.Name))
		}
		if workload.Image != "" && !api.ValidProjectImage(workload.Image) {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q has an invalid image reference", workload.Name))
		}
		if err := workload.ImageHealthcheck.Validate(); err != nil {
			reasons = append(reasons, fmt.Sprintf("workload %q has an invalid image healthcheck: %s", workload.Name, err))
		}
		if api.IsReservedAppSlug(workload.Name) {
			// Do not strand a project that already owns a reserved collision:
			// it must remain deployable while the operator migrates it. Only a
			// fresh allocation (or a collision outside this exact member) is
			// rejected.
			app, exists := bySlug[workload.Name]
			if !exists || projectID == "" || app.ProjectID != projectID || app.WorkloadName != workload.Name {
				reasons = append(reasons, fmt.Sprintf(
					"workload %q uses app slug %q reserved for a Gregale service",
					workload.Name, workload.Name))
				continue
			}
		}
		if !api.ValidAppSlug(workload.Name) {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q has invalid app slug %q; use 3-40 lowercase letters, digits, or hyphens",
				workload.Name, workload.Name))
			continue
		}
		if firstRoot, duplicate := seen[workload.Name]; duplicate {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q produces a duplicate app slug for roots %q and %q; use distinct declared package names",
				workload.Name, firstRoot, workload.RootDir))
			continue
		}
		seen[workload.Name] = workload.RootDir
		if app, exists := bySlug[workload.Name]; exists &&
			(projectID == "" || app.ProjectID != projectID || app.WorkloadName != workload.Name) {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q conflicts with existing app slug %q outside this project member",
				workload.Name, app.Slug))
		}
	}
	reasons = append(reasons, reposcan.DependencyValidationReasons(workloads, managed)...)
	return reasons
}

func validateWorkloadAdmissionWithManaged(workloads []reposcan.Workload, managed []reposcan.Managed, accountApps []state.App, projectID string) error {
	reasons := WorkloadAdmissionReasonsWithManaged(workloads, managed, accountApps, projectID)
	if len(reasons) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidWorkloadPlan, strings.Join(reasons, "; "))
}
