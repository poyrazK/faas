package main

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewProjectEnvironmentClone(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projectSlug := r.PathValue("slug")
	source := r.PathValue("environment")
	target := strings.TrimSpace(r.URL.Query().Get("to"))
	if !api.ValidProjectEnvironmentSlug(source) || !api.ValidProjectEnvironmentSlug(target) || source == target {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid environment clone", "to must name a different valid project environment"))
		return
	}
	shareResources := false
	if raw := strings.TrimSpace(r.URL.Query().Get("share_resources")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid resource sharing option", "share_resources must be true or false"))
			return
		}
		shareResources = parsed
	}

	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	snapshot, problem := s.loadProjectEnvironmentState(r.Context(), acct, projectSlug, source)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	plan := api.ProjectEnvironmentClonePlanResponse{
		ProjectSlug: projectSlug, FromEnvironment: source, ToEnvironment: target,
		ShareResources: shareResources, CanClone: true, CanPromote: len(snapshot.Workloads) > 0,
		WorkloadCount: len(snapshot.Workloads), Actions: []api.ProjectEnvironmentClonePlanActionResponse{},
		BlockingReasons: []string{}, Warnings: []string{},
	}
	if _, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, target); err == nil {
		plan.CanClone = false
		plan.BlockingReasons = append(plan.BlockingReasons, "The target environment already exists.")
	} else if !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not check the target environment"))
		return
	}

	if snapshot.Configuration.Version > 0 {
		plan.Actions = append(plan.Actions, api.ProjectEnvironmentClonePlanActionResponse{
			Resource: "configuration", Action: "copy", Count: 1,
		})
	}
	plan.Actions = append(plan.Actions, api.ProjectEnvironmentClonePlanActionResponse{
		Resource: "workloads", Action: "reuse", Count: len(snapshot.Workloads),
		Reason: "Project workloads are shared; the new environment adds a separate runtime scope.",
	})
	variables, customerSecrets := 0, 0
	for _, workload := range snapshot.Workloads {
		variables += len(workload.Variables)
		for _, secret := range workload.Secrets {
			if secret.ManagedBy == "" {
				customerSecrets++
			}
		}
	}
	if variables > 0 {
		plan.Actions = append(plan.Actions, api.ProjectEnvironmentClonePlanActionResponse{
			Resource: "runtime_variables", Action: "copy", Count: variables,
		})
	}
	if customerSecrets > 0 {
		plan.Actions = append(plan.Actions, api.ProjectEnvironmentClonePlanActionResponse{
			Resource: "customer_secrets", Action: "copy_sealed", Count: customerSecrets,
			Reason: "Secret values remain sealed and are never included in this plan.",
		})
	}

	resourcePlans, resourceErr := s.planProjectEnvironmentBindingClones(r.Context(), acct, project, source, shareResources)
	if resourceErr != nil {
		plan.CanClone = false
		plan.BlockingReasons = append(plan.BlockingReasons, projectEnvironmentClonePlanBlocker(resourceErr))
	} else {
		counts := map[string]int{}
		for _, binding := range resourcePlans {
			counts[binding.app.Slug+"\x00"+binding.kind]++
		}
		keys := make([]string, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts := strings.SplitN(key, "\x00", 2)
			action, reason := "isolate", "Create a target-scoped binding with isolated data."
			if shareResources {
				action, reason = "share", "Create fresh target credentials that intentionally access the source data."
			}
			plan.Actions = append(plan.Actions, api.ProjectEnvironmentClonePlanActionResponse{
				WorkloadSlug: parts[0], Resource: parts[1] + "_data", Action: action, Count: counts[key], Reason: reason,
			})
		}
	}

	for _, workload := range snapshot.Workloads {
		plan.Actions = append(plan.Actions,
			projectEnvironmentClonePlanRouteAction(workload),
			projectEnvironmentClonePlanOwnershipAction(workload.WorkloadSlug, "edge_policies", workload.Policies.Ownership),
			projectEnvironmentClonePlanOwnershipAction(workload.WorkloadSlug, "routing_policies", workload.RoutingPolicies.Ownership),
			projectEnvironmentClonePlanOwnershipAction(workload.WorkloadSlug, "ip_policies", workload.IPPolicies.Ownership),
		)
		applicationDomains, boundDomains := 0, 0
		for _, domain := range workload.Domains {
			if domain.Ownership == "environment" {
				boundDomains++
			} else {
				applicationDomains++
			}
		}
		if applicationDomains > 0 {
			plan.Actions = append(plan.Actions, api.ProjectEnvironmentClonePlanActionResponse{
				WorkloadSlug: workload.WorkloadSlug, Resource: "domains", Action: "shared", Count: applicationDomains,
				Reason: "Application-wide domains stay shared across environments.",
			})
		}
		if boundDomains > 0 {
			plan.Actions = append(plan.Actions, api.ProjectEnvironmentClonePlanActionResponse{
				WorkloadSlug: workload.WorkloadSlug, Resource: "domains", Action: "skip", Count: boundDomains,
				Reason: "Environment-bound domains need separate DNS and certificate verification.",
			})
		}
		if workload.Release.Status == "live" {
			plan.Actions = append(plan.Actions, api.ProjectEnvironmentClonePlanActionResponse{
				WorkloadSlug: workload.WorkloadSlug, Resource: "live_release", Action: "promote", Count: 1,
				Reason: "Create does not copy deployments; the optional bring-up step promotes this immutable artifact.",
			})
		} else {
			plan.CanPromote = false
			plan.Actions = append(plan.Actions, api.ProjectEnvironmentClonePlanActionResponse{
				WorkloadSlug: workload.WorkloadSlug, Resource: "live_release", Action: "not_available",
				Reason: "No live source release is available to promote.",
			})
		}
	}
	plan.Warnings = append(plan.Warnings,
		"Account quotas are rechecked during creation; this read-only plan does not reserve capacity.",
		"Custom environment-bound domains are not copied; configure and verify each hostname separately.",
	)
	if !plan.CanPromote {
		plan.Warnings = append(plan.Warnings, "One or more workloads lack a live source release; clone can proceed, but one-command bring-up cannot.")
	}
	writeJSON(w, http.StatusOK, plan)
}

func projectEnvironmentClonePlanOwnershipAction(workload, resource, ownership string) api.ProjectEnvironmentClonePlanActionResponse {
	if ownership == "environment" {
		return api.ProjectEnvironmentClonePlanActionResponse{
			WorkloadSlug: workload, Resource: resource, Action: "copy",
			Reason: "Explicit environment-owned state is copied to the target.",
		}
	}
	return api.ProjectEnvironmentClonePlanActionResponse{
		WorkloadSlug: workload, Resource: resource, Action: "shared",
		Reason: "No explicit environment-owned override exists; the application-wide behavior remains effective.",
	}
}

func projectEnvironmentClonePlanRouteAction(workload api.ProjectEnvironmentStateWorkloadResponse) api.ProjectEnvironmentClonePlanActionResponse {
	if workload.Routes.Ownership == "environment" || !workload.Routes.OnlyAllowDeclaredRoutes || len(workload.Routes.DeclaredRoutes) > 0 {
		return api.ProjectEnvironmentClonePlanActionResponse{
			WorkloadSlug: workload.WorkloadSlug, Resource: "routes", Action: "copy",
			Reason: "The declared-route contract is snapshotted into the target environment.",
		}
	}
	return api.ProjectEnvironmentClonePlanActionResponse{
		WorkloadSlug: workload.WorkloadSlug, Resource: "routes", Action: "shared",
		Reason: "Route enforcement relies on the app-wide OpenAPI contract, which is not copied as environment-owned state.",
	}
}

func projectEnvironmentClonePlanBlocker(err error) string {
	switch {
	case errors.Is(err, errIsolatedObjectStorageCloneUnsupported):
		return "The object-storage provider cannot make an isolated copy; use --share-resources only if sharing source data is intentional."
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		return "The plan does not allow the isolated managed PostgreSQL copy."
	case errors.Is(err, managedpostgres.ErrUnavailable), errors.Is(err, managedpostgres.ErrUnsupported):
		return "Managed PostgreSQL is unavailable or cannot create an isolated copy."
	default:
		return "A managed resource is unavailable or not ready; inspect the source binding and retry the plan."
	}
}
