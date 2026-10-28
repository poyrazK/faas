package edgerules

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// CORS presets (ADR-129) are baked into the kind=cors rules that name them
// when a gateway compiles a host, so editing or deleting one changes served
// policy exactly as editing those rules does. It goes through the same
// generation barrier, so the API answers only after the serving fleet applied
// it (ADR-091 amendment 2026-10-28); it used to rely on a best-effort
// notification alone.

// PrepareCorsPresetChange fences the hosts of the account's rules that use
// the preset before the preset is written. The returned release drops the
// account's edge-rule lock and must run once the write and apply are done.
func (s *Service) PrepareCorsPresetChange(ctx context.Context, accountID, presetID string) ([]*EdgeRuleConvergence, func(context.Context), *api.Problem) {
	return s.prepareReferencedRuleChange(ctx, "cors_preset_changed",
		api.ErrCapacity("edge-rule fleet convergence is unavailable; the CORS preset was not changed"),
		func(ctx context.Context) (map[string][]string, error) {
			return s.corsPresetHosts(ctx, accountID, presetID)
		})
}

// ApplyCorsPresetChange releases the fences PrepareCorsPresetChange set, after
// the preset was written.
func (s *Service) ApplyCorsPresetChange(ctx context.Context, w http.ResponseWriter, presetID string, convergences []*EdgeRuleConvergence) *api.Problem {
	return s.applyReferencedRuleChange(ctx, w, "CORS preset", presetID, convergences)
}

// AbortCorsPresetChange drops the fences of a preset change that was not
// written.
func AbortCorsPresetChange(ctx context.Context, convergences []*EdgeRuleConvergence) {
	abortEdgeRuleConvergences(ctx, convergences)
}

// SharedDomainCORSOriginProblem refuses a credentialed CORS policy whose
// allowed origins include a subdomain wildcard under a domain the platform
// owns, such as "https://*.<apps domain>" (ADR-091 amendment 2026-10-28).
// Every customer's app is served one label under the apps domain, so such an
// entry lets any other customer's app send the visitor's cookies and read the
// response. It applies to kind=cors rules and CORS presets; an origin under a
// customer's own domain, and an uncredentialed policy, are not affected.
func SharedDomainCORSOriginProblem(origins []string, allowCredentials bool) *api.Problem {
	if !allowCredentials {
		return nil
	}
	domains := hostidentity.Configured()
	for _, origin := range origins {
		_, rest, ok := strings.Cut(strings.ToLower(origin), "://")
		if !ok {
			continue
		}
		suffix, wildcard := strings.CutPrefix(rest, "*.")
		if !wildcard {
			continue
		}
		if host, _, hasPort := strings.Cut(suffix, ":"); hasPort {
			suffix = host
		}
		if domains.Owns(suffix) {
			return api.ErrValidation(fmt.Sprintf("allow_origins entry %q covers every app on the shared %s domain, so with allow_credentials any other customer's app could read responses with your visitors' cookies; list your app origins instead", origin, suffix))
		}
	}
	return nil
}

// corsPresetHosts maps each app with a rule that uses the preset to those
// rules' match hosts.
func (s *Service) corsPresetHosts(ctx context.Context, accountID, presetID string) (map[string][]string, error) {
	rules, err := s.Host.Store().ListEdgeRulesForAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	hosts := map[string][]string{}
	for _, rule := range rules {
		usesPreset := rule.CorsPresetID != nil && *rule.CorsPresetID == presetID
		if !usesPreset && rule.Action.CORS != nil && rule.Action.CORS.CorsPresetID != nil {
			usesPreset = *rule.Action.CORS.CorsPresetID == presetID
		}
		if usesPreset {
			hosts[rule.AppID] = append(hosts[rule.AppID], rule.MatchHost)
		}
	}
	return hosts, nil
}
