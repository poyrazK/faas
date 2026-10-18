package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

type ServiceProxyAsyncAdmitter func(http.ResponseWriter, *http.Request, ServiceCaller, ServiceTarget, string) bool
type RehearsalAsyncStore interface {
	AsyncRouteInvocationStore
	AccountByID(context.Context, string) (state.Account, error)
	ScenarioTestMemberByApp(context.Context, string) (state.ScenarioTestMember, error)
	ListEdgeRulesForApp(context.Context, string) ([]state.EdgeRule, error)
}
type rehearsalAsyncEnqueuer struct{ store RehearsalAsyncStore }

func (e rehearsalAsyncEnqueuer) EnqueueAsyncRoute(ctx context.Context, r AsyncRouteRequest) (AsyncRouteAccepted, error) {
	a, err := EnqueueAsyncRoute(ctx, e.store, r)
	if err != nil {
		return a, err
	}
	j, err := e.store.InvocationByID(ctx, a.ID)
	if err != nil {
		return a, err
	}
	if j.State == state.InvocationCancelled {
		return AsyncRouteAccepted{}, state.ErrConflict
	}
	return a, nil
}

// NewRehearsalAsyncAdmitter runs only after private service authorization. It
// honors durable enrollment even when operators disable new worker admission.
func NewRehearsalAsyncAdmitter(store RehearsalAsyncStore) ServiceProxyAsyncAdmitter {
	return func(w http.ResponseWriter, r *http.Request, caller ServiceCaller, target ServiceTarget, path string) bool {
		if caller.RehearsalRunID == "" || target.ScenarioTestRunID == "" {
			return false
		}
		m, err := store.ScenarioTestMemberByApp(r.Context(), target.AppID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("managed worker membership unavailable"))
			return true
		}
		if m.ManagedWorkJSON == "" {
			return false
		}
		if m.RunID != caller.RehearsalRunID || m.RunID != target.ScenarioTestRunID || m.AccountID != caller.AccountID {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Worker unavailable", "worker belongs to another rehearsal"))
			return true
		}
		participant, _ := rehearsalwork.Decode(m.ManagedWorkJSON).Participant(m.Workload)
		if !participant.Async {
			return false
		}
		rules, err := store.ListEdgeRulesForApp(r.Context(), target.AppID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("managed worker routes unavailable"))
			return true
		}
		for _, v := range rules {
			if v.Enabled && v.Kind == state.EdgeRuleKindAsync && v.Match != nil {
				api.WriteProblem(w, api.ErrValidation("managed worker rehearsals do not support advanced async route conditions"))
				return true
			}
		}
		matched := rehearsalAsyncMatch(rules, path, r)
		if matched == nil {
			return false
		}
		return serveRehearsalAsync(store, w, r, caller, target, path, matched)
	}
}

func rehearsalAsyncMatch(rules []state.EdgeRule, path string, r *http.Request) *EdgeRuleAsyncResolved {
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Priority == rules[j].Priority {
			return rules[i].ID < rules[j].ID
		}
		return rules[i].Priority < rules[j].Priority
	})
	compiled := []EdgeRuleAsyncResolved{}
	for _, v := range rules {
		if !v.Enabled || v.Kind != state.EdgeRuleKindAsync || v.Action.Async == nil {
			continue
		}
		a := v.Action.Async
		methods := map[string]bool{}
		for _, m := range v.MatchMethods {
			methods[m] = true
		}
		if len(methods) == 0 {
			methods = nil
		}
		compiled = append(compiled, EdgeRuleAsyncResolved{ID: v.ID, AccountID: v.AccountID, AppID: v.AppID, PathGlob: v.MatchPath, Methods: methods, MatchHeaders: v.MatchHeaders, OnSuccessWebhook: a.OnSuccess, OnFailureWebhook: a.OnFailure, RetryPolicy: a.RetryPolicy, MaxAgeSeconds: a.MaxAgeSeconds})
	}
	return PickFirstAsyncMatch(compiled, path, r.Method, r.Header)
}

func serveRehearsalAsync(store RehearsalAsyncStore, w http.ResponseWriter, r *http.Request, caller ServiceCaller, target ServiceTarget, path string, rule *EdgeRuleAsyncResolved) bool {
	_, release, _ := serviceReleaseFromRequest(r)
	_, override, _ := serviceDeploymentOverrideFromRequest(r)
	if release || override || isUpgradeRequest(r) || rule.OnSuccessWebhook != "" || rule.OnFailureWebhook != "" {
		api.WriteProblem(w, api.ErrValidation("managed worker rehearsals do not support release overrides, upgrades or callback routes"))
		return true
	}
	app, err := store.AppByID(r.Context(), target.AppID)
	if err != nil || app.AccountID != caller.AccountID {
		api.WriteProblem(w, api.ErrCapacity("managed worker unavailable"))
		return true
	}
	account, err := store.AccountByID(r.Context(), app.AccountID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("managed worker account unavailable"))
		return true
	}
	payload, problem := readAsyncRoutePayload(r, rehearsalwork.MaxPayloadBytes)
	if problem != nil {
		api.WriteProblem(w, problem)
		return true
	}
	request := r.Clone(r.Context())
	u := *r.URL
	u.Path, u.RawPath = path, ""
	request.URL = &u
	request.Body = io.NopCloser(bytes.NewReader(payload))
	request.GetBody = nil
	request.ContentLength = int64(len(payload))
	h := Handler{asyncRoutes: rehearsalAsyncEnqueuer{store: store}}
	return h.applyEdgeRuleAsync(w, request, App{ID: app.ID, AccountID: app.AccountID, Plan: account.Plan, Scope: state.DefaultEnvScope, RequestInvocationsEnabled: app.AcceptsRequestInvocations()}, rule)
}
