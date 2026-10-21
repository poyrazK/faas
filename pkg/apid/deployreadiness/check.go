package deployreadiness

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reader deliberately excludes writes. Optional release readers are tested at
// their use sites; missing readers produce unknown checks.
type Reader interface {
	ProjectBySlug(context.Context, string, string) (state.Project, error)
	AppsForProject(context.Context, string, string) ([]state.App, error)
	CountDeployedApps(context.Context, string) (int, error)
	ProjectEnvironmentBySlug(context.Context, string, string, string) (state.ProjectEnvironment, error)
	AppByID(context.Context, string) (state.App, error)
	ListAppEnvInScope(context.Context, string, string, string) ([]state.AppEnv, error)
	ListAppSecretsInScope(context.Context, string, string, string) ([]state.AppSecret, error)
}

type Config struct {
	ServiceAdditionsEnabled  bool
	WorkerReleasesEnabled    bool
	ProgressiveEnabled       bool
	SecretsEnabled           bool
	CriticalProbesConfigured bool
	PromotionPreview         func(context.Context, state.Account, string, api.ProjectDeploymentCheckRequest) (api.ProjectEnvironmentPromotionPreviewResponse, *api.Problem)
}

func Check(ctx context.Context, store Reader, account state.Account, projectSlug string, req api.ProjectDeploymentCheckRequest, config Config) api.ProjectDeploymentReadiness {
	r := newReport(projectSlug, req)
	r.identities["account"], r.identities["plan"] = account.ID, account.Plan
	r.identities["capabilities"] = [5]bool{config.WorkerReleasesEnabled, config.ProgressiveEnabled, config.SecretsEnabled, config.CriticalProbesConfigured, config.ServiceAdditionsEnabled}
	if _, ok := api.LimitsFor(account.Plan); !ok {
		r.add("account_plan_unavailable", "unknown", "", "The account plan could not be evaluated.", "Retry after the account plan is available.")
		return r.finish(req)
	}
	r.identities["limits"] = api.MustLimitsFor(account.Plan)
	if req.FromEnvironment != "" {
		checkPromotion(ctx, r, store, account, req, config)
		return r.finish(req)
	}
	checkMode(r, req, config)
	scan := sourceScan(req)
	reasons := reposcan.DependencyValidationReasons(scan.Workloads, scan.Managed)
	for _, reason := range reasons {
		r.add("dependency_invalid", "blocked", "", reason, "Correct the declared project dependency graph.")
	}
	if len(reasons) == 0 {
		r.add("dependencies_valid", "passed", "", "Declared dependencies form a valid graph.", "")
	}
	project, err := store.ProjectBySlug(ctx, account.ID, projectSlug)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		r.add("project_unavailable", "unknown", "", "Project state could not be read.", "Retry the check when project state is available.")
		return r.finish(req)
	}
	var apps []state.App
	if errors.Is(err, state.ErrNotFound) && req.Environment != "production" && req.Environment != "default" {
		r.add("environment_unavailable", "blocked", "", "A new project does not yet own the selected environment.", "Create the project and register the environment before deploying to it.")
	}
	if err == nil {
		r.identities["project"] = project.ID
		apps, err = store.AppsForProject(ctx, account.ID, project.ID)
		if err != nil {
			r.add("workloads_unavailable", "unknown", "", "Project workloads could not be read.", "Retry the check when workload state is available.")
			return r.finish(req)
		}
		environment, envErr := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, req.Environment)
		if envErr != nil {
			status := "unknown"
			if errors.Is(envErr, state.ErrNotFound) {
				status = "blocked"
			}
			r.add("environment_unavailable", status, "", "The selected project environment is unavailable.", "Select an existing environment or create it before deploying.")
		} else {
			r.identities["environment"] = environment
			if environment.Protected {
				r.add("environment_approval_required", "blocked", "", "The selected environment requires an approval bound to the final deployment plan.", "Obtain approval for the final scan/apply plan; readiness does not issue or consume approval credentials.")
			}
		}
	}
	appCount, appErr := store.CountDeployedApps(ctx, account.ID)
	if appErr != nil {
		r.add("app_quota_unavailable", "unknown", "", "Account app usage could not be read.", "Retry when account usage is available.")
	} else {
		r.identities["app_count"] = appCount
		newApps := 0
		for _, workload := range req.Workloads {
			if findApp(apps, workload.Workload) == nil {
				newApps++
			}
		}
		r.quota("app_quota", appCount+newApps, api.MustLimitsFor(account.Plan).DeployedApps, "App usage including new project workloads.", "Remove unused apps or select a plan with enough app slots.")
	}
	baseline := readBaseline(ctx, r, store, account.ID, project.ID, req)
	for _, workload := range req.Workloads {
		checkWorkload(ctx, r, store, account, req, workload, findApp(apps, workload.Workload), baseline)
	}
	if req.Atomic {
		checkRelease(ctx, r, store, account, project, apps, baseline, req, scan, config)
	}
	return r.finish(req)
}

func findApp(apps []state.App, w api.PlanWorkload) *state.App {
	for i := range apps {
		if apps[i].WorkloadName == w.Name && apps[i].RootDir == w.RootDir && apps[i].PreviewOfSlug == "" {
			return &apps[i]
		}
	}
	return nil
}
func sourceScan(req api.ProjectDeploymentCheckRequest) reposcan.Result {
	var scan reposcan.Result
	for _, item := range req.Workloads {
		w := item.Workload
		scan.Workloads = append(scan.Workloads, reposcan.Workload{Name: w.Name, RootDir: w.RootDir, Image: w.Image, Dockerfile: w.Dockerfile, Class: reposcan.Class(w.Class), Command: w.Command, CommandShell: item.CommandShell,
			ReleaseCommand: item.ReleaseCommand, ReleaseCommandShell: item.ReleaseCommandShell, DependsOn: w.DependsOn, DependsOnConditions: w.DependsOnConditions, EnvKeys: w.EnvKeys, Ports: w.Ports,
			ServiceBindingPolicy: reposcan.ServiceBindingPolicy(w.ServiceBindingPolicy), ServiceBindingTransport: reposcan.ServiceBindingTransport(w.ServiceBindingTransport), ServiceReliability: w.ServiceReliability,
			PreviewServiceCallsPolicy: reposcan.PreviewServiceCallsPolicy(w.PreviewServiceCallsPolicy), AllowedServiceCallers: w.AllowedServiceCallers, AllowedServiceCallScopes: w.AllowedServiceCallScopes, PlatformTenantRequired: w.PlatformTenantRequired})
	}
	for _, name := range req.Managed {
		scan.Managed = append(scan.Managed, reposcan.Managed{Name: name})
	}
	return scan
}

func ValidateRequest(project string, req api.ProjectDeploymentCheckRequest) bool {
	if !api.ValidProjectSlug(project) || !api.ValidProjectEnvironmentSlug(req.Environment) || len(req.Workloads) > api.ProjectReleaseSetMaxMembers || len(req.Managed) > api.ProjectReleaseSetMaxMembers {
		return false
	}
	if req.ExpectedStateHash != "" && (len(req.ExpectedStateHash) != 64 || strings.Trim(req.ExpectedStateHash, "0123456789abcdef") != "") {
		return false
	}
	if req.FromEnvironment != "" {
		return api.ValidProjectEnvironmentSlug(req.FromEnvironment) && req.FromEnvironment != req.Environment && len(req.Workloads) == 0 && len(req.Managed) == 0 && len(req.ReleaseConfig) == 0 && !req.Atomic && !req.AddServices && !req.WorkerHealth && !req.Progressive && !req.Stabilize
	}
	if len(req.Workloads) == 0 {
		return false
	}
	names := map[string]bool{}
	for _, item := range req.Workloads {
		w := item.Workload
		if !api.ValidProjectSlug(w.Name) || names[w.Name] || w.RootDir != "" && !fs.ValidPath(w.RootDir) {
			return false
		}
		names[w.Name] = true
		if len(w.EnvKeys) > api.ProjectDeploymentCheckMaxKeys || len(item.ProvidedKeys) > api.ProjectDeploymentCheckMaxKeys || len(w.DependsOn) > api.ProjectReleaseSetMaxMembers {
			return false
		}
		for _, dependency := range w.DependsOn {
			if !api.ValidProjectSlug(dependency) {
				return false
			}
		}
		for _, key := range append(append([]string{}, w.EnvKeys...), item.ProvidedKeys...) {
			if api.ValidateEnvKey(key) != nil {
				return false
			}
		}
	}
	for _, managed := range req.Managed {
		if !api.ValidProjectSlug(managed) {
			return false
		}
	}
	return true
}

func sortedKeys(keys map[string]bool) []string {
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
