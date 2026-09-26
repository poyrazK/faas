package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

const manifestAsyncRouteKeyPrefix = "async-route:"

type sourceRefManifestEdgeRuleChange struct {
	kind     string // created | updated | deleted
	ruleID   string
	previous state.EdgeRule
	current  state.EdgeRule
}

func (s *server) applySourceRefManifestAsyncRoutes(ctx context.Context, acct state.Account, app state.App, routes []gregalemanifest.AsyncRoute, staged *sourceRefManifestStaged) *api.Problem {
	manage := routes != nil && len(routes) == 0
	var matching []gregalemanifest.AsyncRoute
	for _, route := range routes {
		if route.App == app.Slug {
			manage = true
			matching = append(matching, route)
		}
	}
	if !manage {
		return nil
	}
	if len(matching) > 0 {
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok || !limits.AsyncInvokeAllowed {
			return api.ErrPlanEdgeRuleKindNotAllowed(acct.Plan, string(state.EdgeRuleKindAsync))
		}
		if !app.AcceptsRequestInvocations() {
			return api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode)
		}
	}

	existing, err := s.store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("could not list app edge rules for async_routes")
	}
	existingByKey := make(map[string]state.EdgeRule)
	managedCount := 0
	for _, rule := range existing {
		if !strings.HasPrefix(rule.ManifestKey, manifestAsyncRouteKeyPrefix) {
			continue
		}
		if rule.Kind != state.EdgeRuleKindAsync {
			return api.ErrCapacity("manifest-owned async route has an unexpected edge-rule kind")
		}
		if _, duplicate := existingByKey[rule.ManifestKey]; duplicate {
			return api.ErrCapacity("duplicate manifest-owned async route identity")
		}
		existingByKey[rule.ManifestKey] = cloneSourceRefManifestEdgeRule(rule)
		managedCount++
	}

	desiredByKey := make(map[string]state.CreateEdgeRuleParams, len(matching))
	desiredOrder := make([]string, 0, len(matching))
	for _, route := range matching {
		params, problem := s.sourceRefManifestAsyncRouteParams(ctx, acct, app, route)
		if problem != nil {
			return problem
		}
		key := params.ManifestKey
		desiredByKey[key] = params
		desiredOrder = append(desiredOrder, key)
	}

	// A manifest route never adopts an unowned edge rule implicitly. This
	// preflight catches the common exact-match collision before any mutation.
	for key, desired := range desiredByKey {
		for _, current := range existing {
			if current.ManifestKey == "" && sameEdgeRuleMatch(current, desired) {
				return api.ErrEdgeRuleConflict(fmt.Sprintf("async route %q overlaps unmanaged edge rule %s; remove or change the existing rule first", strings.TrimPrefix(key, manifestAsyncRouteKeyPrefix), current.ID))
			}
		}
	}

	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return api.ErrCapacity("could not resolve account plan limits")
	}
	finalRuleCount := len(existing) - managedCount + len(matching)
	if finalRuleCount > limits.EdgeRulesPerApp {
		return api.ErrPlanLimitEdgeRules(acct.Plan, limits.EdgeRulesPerApp, finalRuleCount)
	}

	// Existing identities are updated in place, preserving the edge-rule ID.
	for _, key := range desiredOrder {
		current, exists := existingByKey[key]
		if !exists {
			continue
		}
		desired := desiredByKey[key]
		if sourceRefManifestEdgeRuleMatches(current, desired) {
			continue
		}
		updated, problem := s.updateSourceRefManifestEdgeRule(ctx, acct, app, current, desired, staged)
		if problem != nil {
			return problem
		}
		existingByKey[key] = updated
	}

	// Remove stale manifest-owned routes before creating replacements so a
	// deploy at the plan's exact edge-rule cap has room to make progress.
	staleKeys := make([]string, 0)
	for key := range existingByKey {
		if _, keep := desiredByKey[key]; !keep {
			staleKeys = append(staleKeys, key)
		}
	}
	sort.Strings(staleKeys)
	for _, key := range staleKeys {
		current := existingByKey[key]
		if _, keep := desiredByKey[current.ManifestKey]; keep {
			continue
		}
		if problem := s.deleteSourceRefManifestEdgeRule(ctx, app, current, staged); problem != nil {
			return problem
		}
	}

	for _, key := range desiredOrder {
		if _, exists := existingByKey[key]; exists {
			continue
		}
		if problem := s.createSourceRefManifestEdgeRule(ctx, acct, app, desiredByKey[key], staged); problem != nil {
			return problem
		}
	}
	return nil
}

func (s *server) sourceRefManifestAsyncRouteParams(ctx context.Context, acct state.Account, app state.App, route gregalemanifest.AsyncRoute) (state.CreateEdgeRuleParams, *api.Problem) {
	methods := append([]string(nil), route.MatchMethods...)
	if len(methods) == 0 {
		methods = []string{"POST"}
	}
	for i := range methods {
		methods[i] = strings.ToUpper(strings.TrimSpace(methods[i]))
	}
	sort.Strings(methods)
	priority := 100
	if route.Priority != nil {
		priority = *route.Priority
	}
	enabled := true
	if route.Enabled != nil {
		enabled = *route.Enabled
	}
	var retry *api.RetryPolicyDTO
	if route.RetryPolicy != nil {
		retry = retryPolicyDTOFromManifest(route.RetryPolicy)
	}
	asyncAction := api.EdgeRuleAsyncAction{
		OnSuccess: route.OnSuccess, OnFailure: route.OnFailure,
		RetryPolicy: retry, MaxAgeSeconds: route.MaxAgeSeconds,
	}
	actionBytes, err := json.Marshal(asyncAction)
	if err != nil {
		return state.CreateEdgeRuleParams{}, api.ErrCapacity("could not encode async route action")
	}
	request := api.CreateEdgeRuleRequest{
		MatchHost: strings.ToLower(strings.TrimSpace(route.MatchHost)),
		MatchPath: route.MatchPath, MatchMethods: methods,
		Priority: &priority, Enabled: &enabled,
		Kind: string(state.EdgeRuleKindAsync), Action: actionBytes,
	}
	if problem := validateEdgeRuleBody(&request, acct.Plan); problem != nil {
		return state.CreateEdgeRuleParams{}, problem
	}
	if problem := s.validateEdgeRuleAsyncDestinations(ctx, app.ID, acct.ID, request.Kind, request.Action); problem != nil {
		return state.CreateEdgeRuleParams{}, problem
	}
	return state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID,
		ManifestKey: manifestAsyncRouteKeyPrefix + route.Name,
		MatchHost:   request.MatchHost, MatchPath: request.MatchPath,
		MatchMethods: methods, Priority: priority, Enabled: enabled,
		Kind: state.EdgeRuleKindAsync,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindAsync, Async: &state.EdgeRuleAsyncAction{
			OnSuccess: asyncAction.OnSuccess, OnFailure: asyncAction.OnFailure,
			RetryPolicy: asyncAction.RetryPolicy, MaxAgeSeconds: asyncAction.MaxAgeSeconds,
		}},
	}, nil
}

func sourceRefManifestEdgeRuleMatches(current state.EdgeRule, desired state.CreateEdgeRuleParams) bool {
	return current.MatchHost == desired.MatchHost && current.MatchPath == desired.MatchPath &&
		stringSlicesEqual(current.MatchMethods, desired.MatchMethods) && current.Priority == desired.Priority &&
		current.Enabled == desired.Enabled && current.Kind == desired.Kind &&
		current.Action.Async != nil && desired.Action.Async != nil &&
		asyncActionsEqual(*current.Action.Async, *desired.Action.Async)
}

func asyncActionsEqual(left, right state.EdgeRuleAsyncAction) bool {
	return left.OnSuccess == right.OnSuccess && left.OnFailure == right.OnFailure &&
		left.MaxAgeSeconds == right.MaxAgeSeconds && retryPoliciesEqualJSON(left.RetryPolicy, right.RetryPolicy)
}

func retryPoliciesEqualJSON(left, right *api.RetryPolicyDTO) bool {
	leftBytes, _ := json.Marshal(left)
	rightBytes, _ := json.Marshal(right)
	return string(leftBytes) == string(rightBytes)
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameEdgeRuleMatch(current state.EdgeRule, desired state.CreateEdgeRuleParams) bool {
	if strings.ToLower(current.MatchHost) != desired.MatchHost || current.MatchPath != desired.MatchPath {
		return false
	}
	if len(current.MatchMethods) == 0 || len(desired.MatchMethods) == 0 {
		return true
	}
	for _, method := range current.MatchMethods {
		for _, wanted := range desired.MatchMethods {
			if strings.EqualFold(method, wanted) {
				return true
			}
		}
	}
	return false
}

func cloneSourceRefManifestEdgeRule(rule state.EdgeRule) state.EdgeRule {
	rule.MatchMethods = append([]string(nil), rule.MatchMethods...)
	if rule.MatchHeaders != nil {
		headers := make(map[string]string, len(rule.MatchHeaders))
		for key, value := range rule.MatchHeaders {
			headers[key] = value
		}
		rule.MatchHeaders = headers
	}
	if rule.Action.Async != nil {
		action := *rule.Action.Async
		if action.RetryPolicy != nil {
			retry := *action.RetryPolicy
			action.RetryPolicy = &retry
		}
		rule.Action.Async = &action
	}
	return rule
}

func sourceRefManifestEdgeRuleUpdate(desired state.CreateEdgeRuleParams) state.UpdateEdgeRuleParams {
	methods := append([]string(nil), desired.MatchMethods...)
	headers := map[string]string{}
	host, path := desired.MatchHost, desired.MatchPath
	priority, enabled := desired.Priority, desired.Enabled
	action := desired.Action
	return state.UpdateEdgeRuleParams{
		MatchHost: &host, MatchPath: &path, MatchMethods: &methods,
		MatchHeaders: &headers, Priority: &priority, Enabled: &enabled, Action: &action,
	}
}

func (s *server) createSourceRefManifestEdgeRule(ctx context.Context, acct state.Account, app state.App, desired state.CreateEdgeRuleParams, staged *sourceRefManifestStaged) *api.Problem {
	convergence, err := s.prepareEdgeRuleMutation(ctx, app.ID, "", "created", desired.MatchHost)
	if err != nil {
		return api.ErrCapacity("async route fleet convergence is unavailable; no route was created")
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		convergence.abort(ctx)
		return api.ErrCapacity("could not resolve account plan limits")
	}
	created, err := s.store.CreateEdgeRuleIfUnderQuota(ctx, desired, limits)
	if err != nil {
		convergence.abort(ctx)
		return sourceRefManifestEdgeRuleStoreProblem(err, acct.Plan)
	}
	staged.edgeRuleChanges = append(staged.edgeRuleChanges, sourceRefManifestEdgeRuleChange{
		kind: "created", ruleID: created.ID, current: cloneSourceRefManifestEdgeRule(created),
	})
	if err := convergence.apply(ctx, created.ID); err != nil {
		return api.ErrCapacity("async route fleet did not converge after creation")
	}
	return nil
}

func (s *server) updateSourceRefManifestEdgeRule(ctx context.Context, acct state.Account, app state.App, previous state.EdgeRule, desired state.CreateEdgeRuleParams, staged *sourceRefManifestStaged) (state.EdgeRule, *api.Problem) {
	convergence, err := s.prepareEdgeRuleMutation(ctx, app.ID, previous.ID, "updated", previous.MatchHost, desired.MatchHost)
	if err != nil {
		return state.EdgeRule{}, api.ErrCapacity("async route fleet convergence is unavailable; no route was updated")
	}
	updated, err := s.store.UpdateEdgeRule(ctx, previous.ID, sourceRefManifestEdgeRuleUpdate(desired))
	if err != nil {
		convergence.abort(ctx)
		return state.EdgeRule{}, api.ErrCapacity("could not update manifest async route")
	}
	staged.edgeRuleChanges = append(staged.edgeRuleChanges, sourceRefManifestEdgeRuleChange{
		kind: "updated", ruleID: updated.ID, previous: cloneSourceRefManifestEdgeRule(previous), current: cloneSourceRefManifestEdgeRule(updated),
	})
	if err := convergence.apply(ctx, updated.ID); err != nil {
		return updated, api.ErrCapacity("async route fleet did not converge after update")
	}
	return updated, nil
}

func (s *server) deleteSourceRefManifestEdgeRule(ctx context.Context, app state.App, previous state.EdgeRule, staged *sourceRefManifestStaged) *api.Problem {
	convergence, err := s.prepareEdgeRuleMutation(ctx, app.ID, previous.ID, "deleted", previous.MatchHost)
	if err != nil {
		return api.ErrCapacity("async route fleet convergence is unavailable; no route was deleted")
	}
	if err := s.store.DeleteEdgeRule(ctx, previous.ID); err != nil {
		convergence.abort(ctx)
		return api.ErrCapacity("could not remove stale manifest async route")
	}
	staged.edgeRuleChanges = append(staged.edgeRuleChanges, sourceRefManifestEdgeRuleChange{
		kind: "deleted", ruleID: previous.ID, previous: cloneSourceRefManifestEdgeRule(previous),
	})
	if err := convergence.apply(ctx, previous.ID); err != nil {
		return api.ErrCapacity("async route fleet did not converge after deletion")
	}
	return nil
}

func (s *server) rollbackSourceRefManifestEdgeRule(ctx context.Context, appID string, change sourceRefManifestEdgeRuleChange) error {
	switch change.kind {
	case "created":
		convergence, err := s.prepareEdgeRuleMutation(ctx, appID, change.ruleID, "deleted", change.current.MatchHost)
		if err != nil {
			return err
		}
		if err := s.store.DeleteEdgeRule(ctx, change.ruleID); err != nil {
			convergence.abort(ctx)
			if errors.Is(err, state.ErrNotFound) {
				return nil
			}
			return err
		}
		return convergence.apply(ctx, change.ruleID)
	case "updated":
		convergence, err := s.prepareEdgeRuleMutation(ctx, appID, change.ruleID, "updated", change.current.MatchHost, change.previous.MatchHost)
		if err != nil {
			return err
		}
		if _, err := s.store.UpdateEdgeRule(ctx, change.ruleID, sourceRefManifestEdgeRuleUpdate(edgeRuleCreateParams(change.previous))); err != nil {
			convergence.abort(ctx)
			return err
		}
		return convergence.apply(ctx, change.ruleID)
	case "deleted":
		convergence, err := s.prepareEdgeRuleMutation(ctx, appID, "", "created", change.previous.MatchHost)
		if err != nil {
			return err
		}
		app, err := s.store.AppByID(ctx, appID)
		if err != nil {
			convergence.abort(ctx)
			return err
		}
		acct, err := s.store.AccountByID(ctx, app.AccountID)
		if err != nil {
			convergence.abort(ctx)
			return err
		}
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok {
			convergence.abort(ctx)
			return errors.New("could not resolve account plan limits during async route rollback")
		}
		created, err := s.store.CreateEdgeRuleIfUnderQuota(ctx, edgeRuleCreateParams(change.previous), limits)
		if err != nil {
			convergence.abort(ctx)
			return err
		}
		return convergence.apply(ctx, created.ID)
	default:
		return fmt.Errorf("unknown manifest edge-rule rollback change %q", change.kind)
	}
}

func edgeRuleCreateParams(rule state.EdgeRule) state.CreateEdgeRuleParams {
	return state.CreateEdgeRuleParams{
		AccountID: rule.AccountID, AppID: rule.AppID, ManifestKey: rule.ManifestKey,
		MatchHost: rule.MatchHost, MatchPath: rule.MatchPath,
		MatchMethods: append([]string(nil), rule.MatchMethods...),
		MatchHeaders: rule.MatchHeaders, Priority: rule.Priority, Enabled: rule.Enabled,
		Kind: rule.Kind, Action: rule.Action, CorsPresetID: rule.CorsPresetID,
		ValidateMode: rule.ValidateMode,
	}
}

func sourceRefManifestEdgeRuleStoreProblem(err error, plan api.Plan) *api.Problem {
	var quota *state.EdgeRuleQuotaError
	if errors.As(err, &quota) {
		if quota.PerKind {
			return api.ErrPlanEdgeRuleKindQuotaReached(plan, quota.Kind, quota.Observed, quota.Limit)
		}
		return api.ErrPlanLimitEdgeRules(plan, quota.Limit, quota.Observed)
	}
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
	}
	if errors.Is(err, state.ErrConflict) {
		return api.ErrEdgeRuleConflict("an edge rule with this manifest identity already exists")
	}
	return api.ErrCapacity("could not apply manifest async route")
}
