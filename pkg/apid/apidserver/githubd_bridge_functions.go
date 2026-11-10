package apidserver

import (
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/functionsrepo"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// bridgeSourceManifest is the manifest a push applies to app. A function app
// of a functions project gets its own function's settings, and its build its
// handler and runtime (ADR-20261108160000 §4); every other app reads the
// source's gregale.yaml as before.
func bridgeSourceManifest(sourcePath string, app state.App, plan api.Plan, agentSteps bool) (functionsrepo.Function, *gregalemanifest.Manifest, *api.Problem) {
	function, ok, err := functionsrepo.Resolve(sourcePath, app)
	if err != nil {
		return functionsrepo.Function{}, nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", err.Error())
	}
	if !ok {
		manifest, problem := loadSourceRefManifest(sourcePath, app, plan, agentSteps)
		return functionsrepo.Function{}, manifest, problem
	}
	if problem := validateManifestAgainstPlan(function.Manifest, plan, agentSteps); problem != nil {
		return functionsrepo.Function{}, nil, problem
	}
	if err := function.Manifest.ValidateForPlan(plan); err != nil {
		return functionsrepo.Function{}, nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", err.Error())
	}
	return function, function.Manifest, nil
}

// withPendingFunctionSteps records on a push's deploy activity the settings
// the push left to `gregale deploy` because they need a person: new inbound
// webhooks, bucket notifications and secrets from the deployer's environment
// (ADR-20261108160000 §5). The activity feed is where the dashboard and
// `gregale org activity` show them.
func withPendingFunctionSteps(activity *state.OrgActivity, steps []gregalemanifest.RepositoryDeferredStep) {
	if activity == nil || len(steps) == 0 {
		return
	}
	data := map[string]any{}
	if len(activity.Data) > 0 {
		_ = json.Unmarshal(activity.Data, &data)
	}
	data["pending_steps"] = steps
	data["pending_steps_command"] = "gregale deploy"
	activity.Data = activityData(data)
}
