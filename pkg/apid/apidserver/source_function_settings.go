package apidserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"
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
		// A PATCH records app.updated; the push changed the same settings
		// (apps/projects review 2026-11-10, P12).
		if s.audit != nil {
			s.audit.Emit(ctx, "app.updated", &acct.ID, map[string]any{
				"app_id": app.ID, "slug": app.Slug, "source": "repository_push",
				"ram_mb": updated.RAMMB, "idle_timeout_s": updated.IdleTimeoutS, "function_concurrency": updated.Manifest.FunctionConcurrency,
			})
		}
		app = updated
	}
	if prob := s.applySourceFunctionEnv(ctx, acct, app, spec, limits, scope); prob != nil {
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
// prune_env, removes keys in that scope it does not declare, as one
// configuration batch: the change gets a config revision (env history and
// rollback see it), the config guard watches it like any other write, and the
// count limit is enforced atomically (ADR-1451; apps/projects review
// 2026-11-10, P12). Other scopes and secrets are never touched.
func (s *server) applySourceFunctionEnv(ctx context.Context, acct state.Account, app state.App, spec gregalemanifest.FunctionSpec, limits api.Limits, scope string) *api.Problem {
	if len(spec.Env) == 0 && !spec.PruneEnv {
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(spec.Env)) {
		if projectsetup.PlatformProvidedEnvKey(key) {
			return api.ErrValidation(fmt.Sprintf("env %s is set by the platform; remove it from gregale.yaml", key))
		}
	}
	current, err := s.store.ListAppEnvInScope(ctx, app.AccountID, app.ID, scope)
	if err != nil {
		return api.ErrCapacity("could not read the function's env")
	}
	request, changed := functionEnvBatch(spec, current)
	if !changed {
		return nil
	}
	batches, ok := s.store.(state.EnvBatchStore)
	revisions, revisionsOK := s.store.(state.ConfigRevisionStore)
	if !ok || !revisionsOK {
		return api.ErrCapacity("atomic configuration is unavailable")
	}
	// A scope written before config history existed first records a
	// baseline revision and refuses the batch; the push has reviewed nothing
	// else, so it retries once against that baseline.
	for attempt := 0; ; attempt++ {
		latest, err := revisions.ListConfigRevisions(ctx, app.ID, scope, 1)
		if err != nil {
			return api.ErrCapacity("could not read the function's configuration revision")
		}
		revision := int64(0)
		if len(latest) == 1 {
			revision = latest[0].Revision
		}
		request.RequestID, request.ExpectedRevision = uuid.NewString(), &revision
		if problem := request.Validate(limits); problem != nil {
			return problem
		}
		_, err = batches.ApplyEnvBatch(ctx, state.EnvBatchChange{AccountID: app.AccountID, AppID: app.ID, Scope: scope,
			Actor: "repository_push:" + acct.ID, Guard: s.configGuardHasTraffic(ctx, app.ID, scope), Limits: limits, Request: request})
		if attempt == 0 && len(latest) == 0 && errors.Is(err, state.ErrEnvBatchRevisionConflict) {
			continue
		}
		return functionEnvBatchProblem(err, app, limits)
	}
}

func functionEnvBatchProblem(err error, app state.App, limits api.Limits) *api.Problem {
	var quota *state.EnvBatchQuotaError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &quota):
		return api.ErrValidation(fmt.Sprintf("the function's env would put %s over the plan's %d variables", app.Slug, limits.EnvVarsMax))
	case errors.Is(err, state.ErrEnvBatchRevisionConflict):
		return api.ErrCapacity("the function's env changed during the push; retry the delivery")
	default:
		return api.ErrCapacity("could not set the function's env")
	}
}

// functionEnvBatch is the batch that brings a scope's env to the function's
// declared env: changed or new keys set and, with prune_env, undeclared keys
// deleted. It reports false when nothing changes.
func functionEnvBatch(spec gregalemanifest.FunctionSpec, current []state.AppEnv) (api.EnvBatchRequest, bool) {
	existing := make(map[string]string, len(current))
	for _, row := range current {
		existing[row.Key] = row.Value
	}
	request := api.EnvBatchRequest{Values: map[string]string{}}
	for key, value := range spec.Env {
		if old, ok := existing[key]; !ok || old != value {
			request.Values[key] = value
		}
	}
	if spec.PruneEnv {
		for _, key := range slices.Sorted(maps.Keys(existing)) {
			if _, declared := spec.Env[key]; !declared {
				request.Delete = append(request.Delete, key)
			}
		}
	}
	if len(request.Values) == 0 {
		request.Values = nil
	}
	return request, len(request.Values)+len(request.Delete) > 0
}
