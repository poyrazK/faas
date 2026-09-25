package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// updateProjectEnvironmentPolicies replaces only the headers/CORS policy for
// the workload's stable environment URL and bound custom domains.
func (s *server) updateProjectEnvironmentPolicies(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, environment, _, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, r.PathValue("slug"), r.PathValue("environment"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	app, problem := s.projectEnvironmentRoutesWorkload(r, acct, project)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var req api.UpdateProjectEnvironmentEdgePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("plan limits not loaded"))
		return
	}
	maxRules := min(20, limits.EdgeRulesPerApp)
	if req.Rules == nil || len(*req.Rules) > maxRules {
		api.WriteProblem(w, api.ErrValidation(fmt.Sprintf("rules must be a complete list of at most %d headers/CORS rules on this plan", maxRules)))
		return
	}
	host := gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, environment.ID, app.ID)
	if host == "" {
		api.WriteProblem(w, api.ErrCapacity("environment URL is unavailable"))
		return
	}
	rules := make([]state.ProjectEnvironmentEdgeRule, 0, len(*req.Rules))
	for _, candidate := range *req.Rules {
		if candidate.Kind != string(state.EdgeRuleKindHeaders) && candidate.Kind != string(state.EdgeRuleKindCORSA) {
			api.WriteProblem(w, api.ErrValidation("environment policies currently support only headers and cors rules"))
			return
		}
		if candidate.MatchPath == "" {
			candidate.MatchPath = "/"
		}
		priority, enabled := candidate.Priority, candidate.Enabled
		check := api.CreateEdgeRuleRequest{
			MatchHost: host, MatchPath: candidate.MatchPath, MatchMethods: candidate.MatchMethods,
			MatchHeaders: candidate.MatchHeaders, Priority: &priority, Enabled: &enabled,
			Kind: candidate.Kind, Action: candidate.Action,
		}
		if problem := validateEdgeRuleBody(&check, acct.Plan); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if candidate.Kind == string(state.EdgeRuleKindCORSA) {
			var cors api.EdgeRuleCORSAction
			_ = json.Unmarshal(candidate.Action, &cors) // validated above
			if cors.CorsPresetID != nil {
				api.WriteProblem(w, api.ErrValidation("environment CORS policies require inline settings; presets are application-owned"))
				return
			}
		}
		rules = append(rules, state.ProjectEnvironmentEdgeRule{
			Kind: state.EdgeRuleKind(check.Kind), MatchPath: check.MatchPath,
			MatchMethods: append([]string(nil), check.MatchMethods...), MatchHeaders: check.MatchHeaders,
			Priority: priority, Enabled: enabled, Action: actionFromBody(check.Kind, check.Action),
		})
	}
	hosts, err := s.projectEnvironmentPolicyHosts(r.Context(), app.ID, environment.ID, host)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not inspect environment-bound domains; no policy was changed"))
		return
	}
	convergence, err := s.prepareEdgeRuleMutation(r.Context(), app.ID, "", "environment_policy_updated", hosts...)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("edge-policy fleet convergence is unavailable; no policy was changed"))
		return
	}
	policy, err := s.store.PutProjectEnvironmentEdgePolicy(r.Context(), state.ProjectEnvironmentEdgePolicy{
		AccountID: acct.ID, ProjectID: project.ID, AppID: app.ID,
		EnvironmentSlug: environment.Slug, Rules: rules,
	})
	if err != nil {
		convergence.abort(r.Context())
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, environment.Slug))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not store environment policies"))
		}
		return
	}
	s.audit.Emit(r.Context(), "project.environment.policies.updated", &acct.ID, map[string]any{
		"project_id": project.ID, "environment": environment.Slug, "app_id": app.ID, "rule_count": len(rules),
	})
	if err := convergence.apply(r.Context(), ""); err != nil {
		convergence.setResponseState(w, "converging")
		api.WriteProblem(w, api.ErrCapacity("policy was saved but the serving fleet has not acknowledged it; read the current state before retrying"))
		return
	}
	convergence.setResponseState(w, "active")
	writeJSON(w, http.StatusOK, projectEnvironmentEdgePolicyResponse(policy))
}

// The convergence fence must cover every hostname that serves this policy,
// not only the platform URL. Otherwise a bound domain could retain cached
// security headers while the platform URL has already converged.
func (s *server) projectEnvironmentPolicyHosts(ctx context.Context, appID, environmentID, stableHost string) ([]string, error) {
	domains, err := s.store.ListDomainsForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	hosts := []string{stableHost}
	for _, domain := range domains {
		if domain.EnvironmentID == environmentID && domain.Verified() {
			hosts = append(hosts, domain.Domain)
		}
	}
	return hosts, nil
}

func projectEnvironmentEdgePolicyResponse(policy state.ProjectEnvironmentEdgePolicy) api.ProjectEnvironmentEdgePolicyResponse {
	out := api.ProjectEnvironmentEdgePolicyResponse{Ownership: "environment", Rules: make([]api.ProjectEnvironmentEdgeRuleResponse, 0, len(policy.Rules))}
	for _, rule := range policy.Rules {
		var action any
		switch rule.Kind {
		case state.EdgeRuleKindHeaders:
			action = rule.Action.Headers
		case state.EdgeRuleKindCORSA:
			action = rule.Action.CORS
		case state.EdgeRuleKindRedirect:
			action = rule.Action.Redirect
		case state.EdgeRuleKindRewrite:
			action = rule.Action.Rewrite
		}
		body, _ := json.Marshal(action)
		out.Rules = append(out.Rules, api.ProjectEnvironmentEdgeRuleResponse{
			Kind: string(rule.Kind), MatchPath: rule.MatchPath,
			MatchMethods: append([]string(nil), rule.MatchMethods...), MatchHeaders: rule.MatchHeaders,
			Priority: rule.Priority, Enabled: rule.Enabled, Action: body,
		})
	}
	return out
}
