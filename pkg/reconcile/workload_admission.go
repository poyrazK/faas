package reconcile

import (
	"errors"
	"fmt"
	"maps"
	"slices"
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

// ProjectImageLifecycleAdmissionReasons keeps background image workloads, and
// built workers, on their worker/job lifecycle and applies the same plan gate
// as standalone apps.
func ProjectImageLifecycleAdmissionReasons(plan api.Plan, workloads []reposcan.Workload) []string {
	var reasons []string
	for _, workload := range workloads {
		mode := scanExecutionMode(workload)
		if workload.Image == "" && mode == "" {
			continue
		}
		manifest := api.AppManifest{ExecutionMode: mode}
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
func WorkloadAdmissionReasonsWithManaged(workloads []reposcan.Workload, managed []reposcan.Managed, accountApps []state.App, projectID string, references ...[]reposcan.Workload) []string {
	if len(workloads) == 0 {
		return []string{EmptyWorkloadPlanReason}
	}

	bySlug := make(map[string]state.App, len(accountApps))
	for _, app := range accountApps {
		bySlug[app.Slug] = app
	}
	seen := make(map[string]string, len(workloads))
	var reasons []string
	for _, workload := range PreserveAppSlugs(workloads, accountApps, projectID) {
		slug := AppSlug(workload)
		for _, app := range accountApps {
			if projectID != "" && app.ProjectID == projectID && app.WorkloadName == workload.Name && app.PreviewOfSlug == "" && app.Status != state.AppDeleted && app.Slug != slug {
				reasons = append(reasons, fmt.Sprintf("workload %q already uses app slug %q; existing project members cannot be renamed during import", workload.Name, app.Slug))
			}
		}
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
		if api.IsReservedAppSlug(slug) {
			// Do not strand a project that already owns a reserved collision:
			// it must remain deployable while the operator migrates it. Only a
			// fresh allocation (or a collision outside this exact member) is
			// rejected.
			app, exists := bySlug[slug]
			if !exists || projectID == "" || app.ProjectID != projectID || app.WorkloadName != workload.Name || app.PreviewOfSlug != "" {
				reasons = append(reasons, fmt.Sprintf(
					"workload %q uses app slug %q reserved for a Gregale service",
					workload.Name, slug))
				continue
			}
		}
		if !api.ValidAppSlug(slug) {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q has invalid app slug %q; use 3-40 lowercase letters, digits, or hyphens",
				workload.Name, slug))
			continue
		}
		if firstRoot, duplicate := seen[slug]; duplicate {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q produces a duplicate app slug for roots %q and %q; choose distinct app names",
				workload.Name, firstRoot, workload.RootDir))
			continue
		}
		seen[slug] = workload.RootDir
		if app, exists := bySlug[slug]; exists &&
			(projectID == "" || app.ProjectID != projectID || app.WorkloadName != workload.Name || app.PreviewOfSlug != "") {
			reasons = append(reasons, fmt.Sprintf(
				"workload %q conflicts with existing app slug %q outside this project member",
				workload.Name, app.Slug))
		}
	}
	reasons = append(reasons, reposcan.DependencyValidationReasons(workloads, managed, references...)...)
	if len(references) > 0 {
		scan := reposcan.Result{Workloads: workloads}
		for _, group := range references {
			scan.DependencyReferences = append(scan.DependencyReferences, group...)
		}
		referenceNames := workloadNameSet(scan.DependencyReferences)
		available := availableServiceNames(scan, state.Project{ID: projectID, AccountID: projectAccountID(accountApps, projectID)}, accountApps)
		for _, workload := range workloads {
			for _, name := range slices.Sorted(maps.Keys(workload.DependsOnConditions)) {
				condition := workload.DependsOnConditions[name]
				_, reference := referenceNames[name]
				if _, ok := available[name]; reference && condition == api.ComposeDependencyHealthy && !ok {
					reasons = append(reasons, fmt.Sprintf("workload %q requires healthy dependency %q; select that dependency or migrate it to this project first", workload.Name, name))
				}
			}
		}
	}
	return reasons
}

func projectAccountID(apps []state.App, projectID string) string {
	for _, app := range apps {
		if projectID != "" && app.ProjectID == projectID {
			return app.AccountID
		}
	}
	return ""
}

func validateWorkloadAdmissionWithManaged(workloads []reposcan.Workload, managed []reposcan.Managed, accountApps []state.App, projectID string, references ...[]reposcan.Workload) error {
	reasons := WorkloadAdmissionReasonsWithManaged(workloads, managed, accountApps, projectID, references...)
	if len(reasons) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidWorkloadPlan, strings.Join(reasons, "; "))
}
