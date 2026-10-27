package apidserver

import (
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/configguard"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// importComposeEnv writes an opted-in project's plain Compose environment
// values into each selected app before any build is admitted
// (ADR-20261023085230). Keys already configured in the scope, as variables
// or secrets, keep their values. Every app's quota is checked before the
// first write; no value reaches a log, audit event or problem.
func (s *server) importComposeEnv(r *http.Request, acct state.Account, projectID, scope string, env reposcan.ComposeEnv, workloads []reposcan.Workload, limits api.Limits) *api.Problem {
	if !env.Import {
		return nil
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, projectID)
	if err != nil {
		return api.ErrCapacity("could not load project configuration targets")
	}
	type pending struct {
		app    state.App
		writes []configguard.VariableWrite
	}
	var plans []pending
	callable := make([]string, 0, len(workloads)+len(apps))
	for _, w := range workloads {
		callable = append(callable, w.Name)
	}
	for _, app := range apps {
		callable = append(callable, app.WorkloadName)
	}
	for _, w := range workloads {
		values := projectsetup.PlanEnvImport(env, w.Name, callable, limits).Values
		if len(values) == 0 {
			continue
		}
		app, ok := projectAppByWorkload(apps, w.Name)
		if !ok {
			return api.ErrCapacity("could not resolve the selected configuration target")
		}
		unset, err := projectsetup.UnsetEnvKeys(r.Context(), s.store, acct.ID, scope, &app, mapKeys(values))
		if err != nil {
			return api.ErrCapacity("could not read project configuration")
		}
		if len(unset) == 0 {
			continue
		}
		count, err := s.store.CountAppEnv(r.Context(), acct.ID, app.ID)
		if err != nil {
			return api.ErrCapacity("could not count env vars")
		}
		if count+len(unset) > limits.EnvVarsMax {
			return api.ErrPlanLimitEnvVars(limits, count+len(unset))
		}
		writes := make([]configguard.VariableWrite, 0, len(unset))
		for _, key := range unset {
			writes = append(writes, configguard.VariableWrite{Key: key, Value: values[key]})
		}
		plans = append(plans, pending{app: app, writes: writes})
	}
	for _, p := range plans {
		change := s.beginConfigChange(r, acct, p.app, scope, "")
		if err := s.applyVariableWrites(r.Context(), acct, p.app, scope, p.writes, nil); err != nil {
			s.abandonConfigChange(r.Context(), change)
			return api.NewProblem(http.StatusServiceUnavailable, "project_configuration_failed", "Project configuration incomplete",
				"Could not import Compose environment values for workload "+p.app.WorkloadName+". No deployments were started; retry the deploy.")
		}
		s.finishConfigChange(r.Context(), change, state.ConfigSourceEnvSet, p.writes[0].Key, false)
		names := make([]string, 0, len(p.writes))
		for _, write := range p.writes {
			names = append(names, write.Key)
		}
		s.audit.Emit(r.Context(), "env.set", &acct.ID, map[string]any{
			"app_id": p.app.ID, "scope": scope, "names": names, "source": "compose_import",
		})
	}
	return nil
}

func projectAppByWorkload(apps []state.App, workload string) (state.App, bool) {
	for _, app := range apps {
		if app.WorkloadName == workload && app.Status != state.AppDeleted && app.PreviewOfSlug == "" {
			return app, true
		}
	}
	return state.App{}, false
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
