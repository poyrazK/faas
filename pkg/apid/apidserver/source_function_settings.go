package apidserver

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourcePush identifies the repository push applying settings: the
// environment it deploys ("" is production) and the GitHub login that pushed.
type sourcePush struct {
	Scope  string
	Pusher string
}

func (p sourcePush) actor() string { return resolvedActorString("github", "", p.Pusher) }

// applySourceFunctionSettings applies a functions-project entry's app
// settings to its function app on a repository push (ADR-20261108160000 §4):
// RAM, idle timeout and concurrency with PATCH's validation, then its
// non-secret env, before the deployment row exists. An omitted key leaves the
// app's setting alone, as `gregale deploy` does. Like the push's sibling
// addresses, these are not undone when the enqueue fails afterwards.
//
// The env is written in the scope the push deploys only. RAM, idle timeout and
// concurrency belong to the app in every environment, so only a production
// push changes them: a push to a branch mapped to staging must not change what
// production runs with.
//
// Each change is recorded as an API change would be: the env as one config
// revision (`gregale env history`, config rollback), and every change in the
// audit log and the organization's activity, attributed to the pusher.
func (s *server) applySourceFunctionSettings(ctx context.Context, acct state.Account, app state.App, spec gregalemanifest.FunctionSpec, push sourcePush) (state.App, *api.Problem) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return app, api.ErrCapacity("could not resolve account plan limits")
	}
	if push.Scope == "" {
		push.Scope = state.DefaultEnvScope
	}
	req, changed := functionSettingsRequest(spec, app)
	if changed && push.Scope == state.DefaultEnvScope {
		updated, prob := s.applySourceFunctionAppSettings(ctx, acct, app, req, limits, push)
		if prob != nil {
			return app, prob
		}
		app = updated
	}
	if prob := s.applySourceFunctionEnv(ctx, acct, app, spec, limits, push); prob != nil {
		return app, prob
	}
	return app, nil
}

// applySourceFunctionAppSettings writes the RAM, idle timeout and concurrency
// in req with PATCH's validation and records the change.
func (s *server) applySourceFunctionAppSettings(ctx context.Context, acct state.Account, app state.App, req api.UpdateAppRequest, limits api.Limits, push sourcePush) (state.App, *api.Problem) {
	if prob := validateUpdateApp(&req, acct, limits, app); prob != nil {
		return app, prob
	}
	params := state.UpdateAppParams{IdleTimeoutS: req.IdleTimeoutS, SetIdleTimeout: req.IdleTimeoutS != nil}
	fields := []string{}
	if req.RAMMB != nil {
		if prob := api.ValidateAppConfig(limits, *req.RAMMB, app.MaxConcurrency); prob != nil {
			return app, prob
		}
		params.RAMMB = req.RAMMB
		fields = append(fields, "ram_mb")
	}
	if req.IdleTimeoutS != nil {
		fields = append(fields, "idle_timeout_s")
	}
	if req.FunctionConcurrency != nil {
		manifest, _ := stateManifestForUpdate(app, &req)
		params.Manifest = manifest
		fields = append(fields, "function_concurrency")
	}
	updated, err := s.store.UpdateApp(ctx, app.ID, params)
	if err != nil {
		return app, api.ErrCapacity("could not apply the function's settings")
	}
	s.audit.Emit(ctx, "app.updated", &acct.ID, map[string]any{"app_id": app.ID, "source": "github_push", "actor": push.actor(), "fields": fields})
	s.recordAppActivity(ctx, nil, acct, updated, s.pushActivity(push, state.OrgActivity{
		Kind: "app.updated", ResourceType: "app", ResourceID: app.ID, ResourceLabel: app.Slug,
		Data: activityData(map[string]any{"fields": fields}),
	}))
	return updated, nil
}

// pushActivity attributes an activity entry to the pusher.
func (s *server) pushActivity(push sourcePush, entry state.OrgActivity) state.OrgActivity {
	entry.ActorType, entry.ActorLabel = "github", push.actor()
	entry.SourceType, entry.SourceID = "github.push", uuid.NewString()
	return entry
}

// applySourceFunctionEnv writes the function's env in the push's scope and,
// with prune_env, removes keys in that scope it does not declare, as one config
// revision. Other scopes and secrets are never touched. A protected
// environment's config changes only through a reviewed change, so a push that
// would change it is refused.
func (s *server) applySourceFunctionEnv(ctx context.Context, acct state.Account, app state.App, spec gregalemanifest.FunctionSpec, limits api.Limits, push sourcePush) *api.Problem {
	if len(spec.Env) == 0 && !spec.PruneEnv {
		return nil
	}
	set, remove, prob := s.sourceFunctionEnvChanges(ctx, app, spec, limits, push.Scope)
	if prob != nil || len(set) == 0 && len(remove) == 0 {
		return prob
	}
	if prob := protectedEnvironmentConfigProblem(ctx, s.store, app.AccountID, app.ProjectID, push.Scope); prob != nil {
		return api.ErrValidation(fmt.Sprintf("%s: the %s environment is protected, so gregale.yaml env changes for %s need a reviewed deploy (`gregale deploy`)", prob.Detail, push.Scope, app.Slug))
	}
	var change *configChange
	if store, ok := s.store.(state.ConfigRevisionStore); ok {
		change = s.beginConfigChangeAs(ctx, store, acct, app, push.Scope, push.actor(), false, "")
	}
	for _, key := range set {
		if err := s.store.UpsertAppEnvInScope(ctx, app.AccountID, app.ID, push.Scope, key, spec.Env[key]); err != nil {
			s.abandonConfigChange(ctx, change)
			return api.ErrCapacity("could not set the function's env")
		}
	}
	for _, key := range remove {
		if err := s.store.DeleteAppEnvInScope(ctx, app.AccountID, app.ID, push.Scope, key); err != nil {
			s.abandonConfigChange(ctx, change)
			return api.ErrCapacity("could not remove the function's env")
		}
	}
	revision := s.finishConfigChange(ctx, change, state.ConfigSourceEnvBatch, "", false)
	var revisionNumber int64
	if revision != nil {
		revisionNumber = revision.Revision
	}
	for _, key := range set {
		s.recordAppActivity(ctx, nil, acct, app, s.pushActivity(push, state.OrgActivity{Kind: "env.set", ResourceType: "environment_variable",
			ResourceID: push.Scope + ":" + key, ResourceLabel: key, Data: activityData(map[string]any{"scope": push.Scope})}))
	}
	for _, key := range remove {
		s.recordAppActivity(ctx, nil, acct, app, s.pushActivity(push, state.OrgActivity{Kind: "env.deleted", ResourceType: "environment_variable",
			ResourceID: push.Scope + ":" + key, ResourceLabel: key, Data: activityData(map[string]any{"scope": push.Scope})}))
	}
	s.audit.Emit(ctx, "env.batch", &acct.ID, map[string]any{"app_id": app.ID, "scope": push.Scope, "source": "github_push",
		"actor": push.actor(), "revision": revisionNumber, "set": set, "deleted": remove})
	return nil
}

// sourceFunctionEnvChanges validates the declared env and returns the keys to
// set (new or changed) and, with prune_env, the undeclared keys to remove.
func (s *server) sourceFunctionEnvChanges(ctx context.Context, app state.App, spec gregalemanifest.FunctionSpec, limits api.Limits, scope string) (set, remove []string, prob *api.Problem) {
	current, err := s.store.ListAppEnvInScope(ctx, app.AccountID, app.ID, scope)
	if err != nil {
		return nil, nil, api.ErrCapacity("could not read the function's env")
	}
	existing := make(map[string]string, len(current))
	for _, row := range current {
		existing[row.Key] = row.Value
	}
	added := 0
	for _, key := range slices.Sorted(maps.Keys(spec.Env)) {
		if prob := api.ValidateEnvKey(key); prob != nil {
			return nil, nil, prob
		}
		if projectsetup.PlatformProvidedEnvKey(key) {
			return nil, nil, api.ErrValidation(fmt.Sprintf("env %s is set by the platform; remove it from gregale.yaml", key))
		}
		if int64(len(spec.Env[key])) > int64(limits.EnvValueMaxBytes) {
			return nil, nil, api.ErrValidation(fmt.Sprintf("env %s is longer than the plan's %d bytes", key, limits.EnvValueMaxBytes))
		}
		value, ok := existing[key]
		if !ok {
			added++
		}
		if !ok || value != spec.Env[key] {
			set = append(set, key)
		}
	}
	if added > 0 {
		count, err := s.store.CountAppEnv(ctx, app.AccountID, app.ID)
		if err != nil {
			return nil, nil, api.ErrCapacity("could not count the function's env")
		}
		if count+added > limits.EnvVarsMax {
			return nil, nil, api.ErrValidation(fmt.Sprintf("the function's env would put %s over the plan's %d variables", app.Slug, limits.EnvVarsMax))
		}
	}
	if spec.PruneEnv {
		for _, key := range slices.Sorted(maps.Keys(existing)) {
			if _, declared := spec.Env[key]; !declared {
				remove = append(remove, key)
			}
		}
	}
	return set, remove, nil
}

// functionSettingsRequest is the PATCH that brings app to the function's
// declared RAM, idle timeout and concurrency.
func functionSettingsRequest(spec gregalemanifest.FunctionSpec, app state.App) (api.UpdateAppRequest, bool) {
	var req api.UpdateAppRequest
	changed := false
	if spec.RAMMB != 0 && spec.RAMMB != app.RAMMB {
		ram := spec.RAMMB
		req.RAMMB, changed = &ram, true
	}
	if spec.IdleTimeoutS != 0 && spec.IdleTimeoutS != app.IdleTimeoutS {
		idle := spec.IdleTimeoutS
		req.IdleTimeoutS, changed = &idle, true
	}
	if spec.Concurrency != 0 && max(app.Manifest.FunctionConcurrency, 1) != spec.Concurrency {
		concurrency := spec.Concurrency
		req.FunctionConcurrency, changed = &concurrency, true
	}
	return req, changed
}
