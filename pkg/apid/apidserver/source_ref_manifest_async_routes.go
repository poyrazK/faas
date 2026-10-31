package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
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

	desired := make([]state.CreateEdgeRuleParams, 0, len(matching))
	for _, route := range matching {
		params, problem := s.sourceRefManifestAsyncRouteParams(ctx, acct, app, route)
		if problem != nil {
			return problem
		}
		desired = append(desired, params)
	}
	return s.reconcileManifestEdgeRules(ctx, acct, app, manifestEdgeRuleSet{
		prefix: manifestAsyncRouteKeyPrefix, label: "async route",
		kinds:   map[state.EdgeRuleKind]bool{state.EdgeRuleKindAsync: true},
		desired: desired, matches: sourceRefManifestEdgeRuleMatches, collides: sameEdgeRuleMatch,
	}, staged)
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
	if problem := edgerules.ValidateEdgeRuleBody(&request, acct.Plan); problem != nil {
		return state.CreateEdgeRuleParams{}, problem
	}
	if problem := s.edgeRules.ValidateEdgeRuleAsyncDestinations(ctx, app.ID, acct.ID, request.Kind, request.Action); problem != nil {
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
	if rule.Action.Route != nil {
		action := *rule.Action.Route
		rule.Action.Route = &action
	}
	if rule.Action.Rewrite != nil {
		action := *rule.Action.Rewrite
		rule.Action.Rewrite = &action
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

func (s *server) createSourceRefManifestEdgeRule(ctx context.Context, label string, acct state.Account, app state.App, desired state.CreateEdgeRuleParams, staged *sourceRefManifestStaged) *api.Problem {
	convergence, err := s.edgeRules.PrepareEdgeRuleMutation(ctx, app.ID, "", "created", desired.MatchHost)
	if err != nil {
		return api.ErrCapacity(label + " fleet convergence is unavailable; no route was created")
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		convergence.Abort(ctx)
		return api.ErrCapacity("could not resolve account plan limits")
	}
	created, err := s.store.CreateEdgeRuleIfUnderQuota(ctx, desired, limits)
	if err != nil {
		convergence.Abort(ctx)
		return sourceRefManifestEdgeRuleStoreProblem(err, acct.Plan, label)
	}
	staged.edgeRuleChanges = append(staged.edgeRuleChanges, sourceRefManifestEdgeRuleChange{
		kind: "created", ruleID: created.ID, current: cloneSourceRefManifestEdgeRule(created),
	})
	if err := convergence.Apply(ctx, created.ID); err != nil {
		return api.ErrCapacity(label + " fleet did not converge after creation")
	}
	return nil
}

func (s *server) updateSourceRefManifestEdgeRule(ctx context.Context, label string, app state.App, previous state.EdgeRule, desired state.CreateEdgeRuleParams, staged *sourceRefManifestStaged) (state.EdgeRule, *api.Problem) {
	convergence, err := s.edgeRules.PrepareEdgeRuleMutation(ctx, app.ID, previous.ID, "updated", previous.MatchHost, desired.MatchHost)
	if err != nil {
		return state.EdgeRule{}, api.ErrCapacity(label + " fleet convergence is unavailable; no route was updated")
	}
	updated, err := s.store.UpdateEdgeRule(ctx, previous.ID, sourceRefManifestEdgeRuleUpdate(desired))
	if err != nil {
		convergence.Abort(ctx)
		return state.EdgeRule{}, api.ErrCapacity("could not update manifest " + label)
	}
	staged.edgeRuleChanges = append(staged.edgeRuleChanges, sourceRefManifestEdgeRuleChange{
		kind: "updated", ruleID: updated.ID, previous: cloneSourceRefManifestEdgeRule(previous), current: cloneSourceRefManifestEdgeRule(updated),
	})
	if err := convergence.Apply(ctx, updated.ID); err != nil {
		return updated, api.ErrCapacity(label + " fleet did not converge after update")
	}
	return updated, nil
}

func (s *server) deleteSourceRefManifestEdgeRule(ctx context.Context, label string, app state.App, previous state.EdgeRule, staged *sourceRefManifestStaged) *api.Problem {
	convergence, err := s.edgeRules.PrepareEdgeRuleMutation(ctx, app.ID, previous.ID, "deleted", previous.MatchHost)
	if err != nil {
		return api.ErrCapacity(label + " fleet convergence is unavailable; no route was deleted")
	}
	if err := s.store.DeleteEdgeRule(ctx, previous.ID); err != nil {
		convergence.Abort(ctx)
		return api.ErrCapacity("could not remove stale manifest " + label)
	}
	staged.edgeRuleChanges = append(staged.edgeRuleChanges, sourceRefManifestEdgeRuleChange{
		kind: "deleted", ruleID: previous.ID, previous: cloneSourceRefManifestEdgeRule(previous),
	})
	if err := convergence.Apply(ctx, previous.ID); err != nil {
		return api.ErrCapacity(label + " fleet did not converge after deletion")
	}
	return nil
}

func (s *server) rollbackSourceRefManifestEdgeRule(ctx context.Context, appID string, change sourceRefManifestEdgeRuleChange) error {
	switch change.kind {
	case "created":
		convergence, err := s.edgeRules.PrepareEdgeRuleMutation(ctx, appID, change.ruleID, "deleted", change.current.MatchHost)
		if err != nil {
			return err
		}
		if err := s.store.DeleteEdgeRule(ctx, change.ruleID); err != nil {
			convergence.Abort(ctx)
			if errors.Is(err, state.ErrNotFound) {
				return nil
			}
			return err
		}
		return convergence.Apply(ctx, change.ruleID)
	case "updated":
		convergence, err := s.edgeRules.PrepareEdgeRuleMutation(ctx, appID, change.ruleID, "updated", change.current.MatchHost, change.previous.MatchHost)
		if err != nil {
			return err
		}
		if _, err := s.store.UpdateEdgeRule(ctx, change.ruleID, sourceRefManifestEdgeRuleUpdate(edgeRuleCreateParams(change.previous))); err != nil {
			convergence.Abort(ctx)
			return err
		}
		return convergence.Apply(ctx, change.ruleID)
	case "deleted":
		convergence, err := s.edgeRules.PrepareEdgeRuleMutation(ctx, appID, "", "created", change.previous.MatchHost)
		if err != nil {
			return err
		}
		app, err := s.store.AppByID(ctx, appID)
		if err != nil {
			convergence.Abort(ctx)
			return err
		}
		acct, err := state.AppResourceAccount(ctx, s.store, app)
		if err != nil {
			convergence.Abort(ctx)
			return err
		}
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok {
			convergence.Abort(ctx)
			return errors.New("could not resolve account plan limits during async route rollback")
		}
		created, err := s.store.CreateEdgeRuleIfUnderQuota(ctx, edgeRuleCreateParams(change.previous), limits)
		if err != nil {
			convergence.Abort(ctx)
			return err
		}
		return convergence.Apply(ctx, created.ID)
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

func sourceRefManifestEdgeRuleStoreProblem(err error, plan api.Plan, label string) *api.Problem {
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
	return api.ErrCapacity("could not apply manifest " + label)
}
