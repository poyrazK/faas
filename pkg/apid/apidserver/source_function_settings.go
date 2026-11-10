package apidserver

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// applySourceFunctionSettings applies a functions-project entry's app
// settings to its function app on a repository push (ADR-20261108160000 §4):
// RAM, idle timeout and concurrency with PATCH's validation, then its
// non-secret env, before the deployment row exists. An omitted key leaves the
// app's setting alone, as `gregale deploy` does. Like the push's sibling
// addresses, these are not undone when the enqueue fails afterwards.
//
// scope is the environment the push deploys ("" is production). The env is
// written in that scope only. RAM, idle timeout and concurrency belong to the
// app in every environment, so only a production push changes them: a push to
// a branch mapped to staging must not change what production runs with.
func (s *server) applySourceFunctionSettings(ctx context.Context, acct state.Account, app state.App, spec gregalemanifest.FunctionSpec, scope string) (state.App, *api.Problem) {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return app, api.ErrCapacity("could not resolve account plan limits")
	}
	if scope == "" {
		scope = state.DefaultEnvScope
	}
	req, changed := functionSettingsRequest(spec, app)
	if changed && scope == state.DefaultEnvScope {
		if prob := validateUpdateApp(&req, acct, limits, app); prob != nil {
			return app, prob
		}
		params := state.UpdateAppParams{IdleTimeoutS: req.IdleTimeoutS, SetIdleTimeout: req.IdleTimeoutS != nil}
		if req.RAMMB != nil {
			if prob := api.ValidateAppConfig(limits, *req.RAMMB, app.MaxConcurrency); prob != nil {
				return app, prob
			}
			params.RAMMB = req.RAMMB
		}
		if req.FunctionConcurrency != nil {
			manifest, _ := stateManifestForUpdate(app, &req)
			params.Manifest = manifest
		}
		updated, err := s.store.UpdateApp(ctx, app.ID, params)
		if err != nil {
			return app, api.ErrCapacity("could not apply the function's settings")
		}
		app = updated
	}
	if prob := s.applySourceFunctionEnv(ctx, app, spec, limits, scope); prob != nil {
		return app, prob
	}
	return app, nil
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

// applySourceFunctionEnv writes the function's env in scope and, with
// prune_env, removes keys in that scope it does not declare. Other scopes and
// secrets are never touched.
func (s *server) applySourceFunctionEnv(ctx context.Context, app state.App, spec gregalemanifest.FunctionSpec, limits api.Limits, scope string) *api.Problem {
	if len(spec.Env) == 0 && !spec.PruneEnv {
		return nil
	}
	current, err := s.store.ListAppEnvInScope(ctx, app.AccountID, app.ID, scope)
	if err != nil {
		return api.ErrCapacity("could not read the function's env")
	}
	existing := make(map[string]string, len(current))
	for _, row := range current {
		existing[row.Key] = row.Value
	}
	added := 0
	for _, key := range slices.Sorted(maps.Keys(spec.Env)) {
		if prob := api.ValidateEnvKey(key); prob != nil {
			return prob
		}
		if projectsetup.PlatformProvidedEnvKey(key) {
			return api.ErrValidation(fmt.Sprintf("env %s is set by the platform; remove it from gregale.yaml", key))
		}
		if int64(len(spec.Env[key])) > int64(limits.EnvValueMaxBytes) {
			return api.ErrValidation(fmt.Sprintf("env %s is longer than the plan's %d bytes", key, limits.EnvValueMaxBytes))
		}
		if _, ok := existing[key]; !ok {
			added++
		}
	}
	if added > 0 {
		count, err := s.store.CountAppEnv(ctx, app.AccountID, app.ID)
		if err != nil {
			return api.ErrCapacity("could not count the function's env")
		}
		if count+added > limits.EnvVarsMax {
			return api.ErrValidation(fmt.Sprintf("the function's env would put %s over the plan's %d variables", app.Slug, limits.EnvVarsMax))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(spec.Env)) {
		if value, ok := existing[key]; ok && value == spec.Env[key] {
			continue
		}
		if err := s.store.UpsertAppEnvInScope(ctx, app.AccountID, app.ID, scope, key, spec.Env[key]); err != nil {
			return api.ErrCapacity("could not set the function's env")
		}
	}
	if spec.PruneEnv {
		for _, key := range slices.Sorted(maps.Keys(existing)) {
			if _, declared := spec.Env[key]; declared {
				continue
			}
			if err := s.store.DeleteAppEnvInScope(ctx, app.AccountID, app.ID, scope, key); err != nil {
				return api.ErrCapacity("could not remove the function's env")
			}
		}
	}
	return nil
}
