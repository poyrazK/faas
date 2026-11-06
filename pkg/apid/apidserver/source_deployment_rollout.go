package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Resolve the lifecycle before choosing release policy. Applying the source
// manifest later still uses the original app, so its writes can be compensated
// if admission fails.
func (s *server) sourceDeploymentApp(r *http.Request, acct state.Account, app state.App, manifest *gregalemanifest.Manifest) (state.App, *api.Problem) {
	if req, ok := manifest.LifecycleRequest(); ok {
		if problem := validateUpdateApp(&req, acct, api.MustLimitsFor(acct.Plan), app); problem != nil {
			return app, problem
		}
		if problem := s.executionModeApprovalProblem(r.Context(), app, &req); problem != nil {
			return app, problem
		}
		if updated, changed := stateManifestForUpdate(app, &req); changed {
			app.Manifest = *updated
		}
	}
	return app, nil
}

// All source transports use the effective lifecycle and the same manifest
// companions and primary-workload dependencies when constructing a release.
func (s *server) prepareSourceDeploymentRollout(r *http.Request, acct state.Account, app state.App, manifest *gregalemanifest.Manifest, req *api.CreateDeploymentRequest) (state.Deployment, *releaseSchedule, *api.Problem) {
	if len(req.Workflows) == 0 && manifest != nil {
		req.Workflows = manifest.Workflows
	}
	if problem := validateWorkflowDefinitionsAgainstPlan(req.Workflows, acct.Plan, s.agentStepsAllowed(acct)); problem != nil {
		return state.Deployment{}, nil, problem
	}
	app, problem := s.sourceDeploymentApp(r, acct, app, manifest)
	if problem != nil {
		return state.Deployment{}, nil, problem
	}
	sched, problem := s.releaseFreezeOrSchedule(r, acct, app, req)
	if problem != nil {
		return state.Deployment{}, nil, problem
	}
	release.ApplyDefault(r.Context(), s.store, s.log, app, acct.Plan, req)
	if problem := s.releaseApprovalCanaryProblem(r.Context(), app, req); problem != nil {
		return state.Deployment{}, nil, problem
	}
	if problem := validateDeploymentTrafficOptions(req, acct.Plan); problem != nil {
		return state.Deployment{}, nil, problem
	}
	if problem := validateDeploymentRollbackOptions(req, acct.Plan); problem != nil {
		return state.Deployment{}, nil, problem
	}
	limits := api.MustLimitsFor(acct.Plan)
	overrides, problem := validateOverrides(req, limits, acct.Plan)
	if problem != nil {
		return state.Deployment{}, nil, problem
	}
	if applied, changed, problem := s.applyManifestWorkloads(req, manifest, acct, limits); problem != nil {
		return state.Deployment{}, nil, problem
	} else if changed {
		overrides = applied
	} else if problem := s.validateAndPlanSidecars(req, acct, limits); problem != nil {
		return state.Deployment{}, nil, problem
	}
	sched.park(req)
	rollout, problem := buildDeploymentForInsert(app, req, overrides, limits, acct.Plan)
	return rollout, sched, problem
}
