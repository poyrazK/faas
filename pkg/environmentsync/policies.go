package environmentsync

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func normalizePolicies(policies []api.EnvironmentPolicy) error {
	if len(policies) > api.EnvironmentGitOpsMaxPolicies {
		return fmt.Errorf("too many environment policies")
	}
	seen := make(map[string]bool)
	for i, policy := range policies {
		if !api.ValidAppSlug(policy.Name) || seen[policy.Name] {
			return fmt.Errorf("policy names must be valid and unique")
		}
		seen[policy.Name] = true
		if policy.MatchPath == "" {
			policy.MatchPath = "/"
		}
		if !strings.HasPrefix(policy.MatchPath, "/") || len(policy.MatchPath) > 2048 || strings.ContainsAny(policy.MatchPath, "\r\n\x00") || policy.Priority < 0 || policy.Priority > 10000 {
			return fmt.Errorf("policy %q has invalid match_path or priority", policy.Name)
		}
		if policy.Enabled == nil {
			enabled := true
			policy.Enabled = &enabled
		}
		methods, err := normalizeMethods(policy.MatchMethods)
		if err != nil {
			return err
		}
		policy.MatchMethods = methods
		policy.MatchHeaders, err = api.NormalizeEdgeRuleMatchHeaders(policy.MatchHeaders)
		if err != nil {
			return fmt.Errorf("policy %q has invalid match headers", policy.Name)
		}
		switch policy.Kind {
		case "headers":
			var action api.EdgeRuleHeadersAction
			if err := decodeStrict(policy.Action, &action); err != nil {
				return fmt.Errorf("policy %q has invalid headers action: %w", policy.Name, err)
			}
			if action.Validate() != nil {
				return fmt.Errorf("policy %q has invalid headers action", policy.Name)
			}
			policy.Action, _ = json.Marshal(action)
		case "cors":
			var action api.EdgeRuleCORSAction
			if err := decodeStrict(policy.Action, &action); err != nil {
				return fmt.Errorf("policy %q has invalid cors action: %w", policy.Name, err)
			}
			if action.CorsPresetID != nil {
				return fmt.Errorf("policy %q uses an application-owned CORS preset", policy.Name)
			}
			action.AllowMethods, err = normalizeMethods(action.AllowMethods)
			if err != nil {
				return err
			}
			slices.Sort(action.AllowOrigins)
			action.AllowOrigins = slices.Compact(action.AllowOrigins)
			slices.Sort(action.AllowHeaders)
			action.AllowHeaders = slices.Compact(action.AllowHeaders)
			slices.Sort(action.ExposeHeaders)
			action.ExposeHeaders = slices.Compact(action.ExposeHeaders)
			if action.Validate() != nil {
				return fmt.Errorf("policy %q has invalid cors action", policy.Name)
			}
			policy.Action, _ = json.Marshal(action)
		default:
			return fmt.Errorf("policy %q kind %q has no environment-scoped adapter", policy.Name, policy.Kind)
		}
		policies[i] = policy
	}
	slices.SortFunc(policies, func(a, b api.EnvironmentPolicy) int {
		if a.Priority < b.Priority {
			return -1
		}
		if a.Priority > b.Priority {
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return nil
}
