// Package agentapproval decides which requests of an agent key need a
// person's approval (ADR-20261104140058). It classifies routes by their
// ServeMux pattern; apid's authentication middleware applies it.
package agentapproval

import (
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Target says what a production request addresses, so the gate can tell a
// production app from a preview.
type Target int

const (
	// TargetNone: the category applies whatever the request addresses.
	TargetNone Target = iota
	// TargetApp: production only when the {slug} app is a production app.
	TargetApp
	// TargetDeployment: production only when the {id} deployment's app is.
	TargetDeployment
	// TargetEdgeRule: production only when the {id} edge rule's app is.
	TargetEdgeRule
)

// Rule is the classification of one route.
type Rule struct {
	Category string
	Target   Target
}

var wildcard = regexp.MustCompile(`\{[^}]*\}`)

// Normalize turns a ServeMux or OpenAPI pattern ("POST /v1/apps/{slug}") into
// the form the tables use, with every wildcard written "{}", so the names a
// route registration and the spec give a parameter do not matter.
func Normalize(pattern string) string {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return pattern
	}
	path = strings.TrimSuffix(wildcard.ReplaceAllString(path, "{}"), "{$}")
	return method + " " + path
}

// exempt DELETE routes undo nothing a person relies on: an agent
// disconnecting itself, aborting its own upload, its developer sandboxes,
// and cancelling a scheduled release. Revoking agent connections needs an
// admin credential, which an agent key never holds: the route refuses the
// agent, so asking a person first would only lead to that refusal.
var exempt = map[string]bool{
	"DELETE /v1/agent-connections/self":       true,
	"DELETE /v1/uploads/{}":                   true,
	"DELETE /v1/deployments/{}/schedule":      true,
	"DELETE /v1/agent-connections/{}":         true,
	"DELETE /v1/orgs/{}/agent-connections/{}": true,
}

// exemptPrefixes are developer sandboxes (gregale dev) an agent creates and
// throws away as it works.
var exemptPrefixes = []string{"/v1/dev/"}

// destructivePosts destroy data through POST.
var destructivePosts = map[string]bool{
	"POST /v1/preview/{}/destroy":                 true,
	"POST /v1/apps/{}/deployments/clear-obsolete": true,
	"POST /v1/triggers/{}/records/{}/drop":        true,
}

// production routes deploy, roll back, restart or move traffic. Edge-rule
// writes are here too (ADR-091 amendment 2026-11-07): a route, redirect,
// maintenance, rewrite or async rule moves or stops a production app's
// traffic as surely as a rollback, and a gate edit opens or closes it.
var production = map[string]Target{
	"POST /v1/apps/{}/deployments":                         TargetApp,
	"POST /v1/apps/{}/deployments/source-ref":              TargetApp,
	"POST /v1/apps/{}/deployments/source-tarball":          TargetApp,
	"POST /v1/apps/{}/deployments/dev-source":              TargetApp,
	"POST /v1/apps/{}/rollback":                            TargetApp,
	"POST /v1/apps/{}/config/rollback":                     TargetApp,
	"POST /v1/apps/{}/rollouts/recover":                    TargetApp,
	"POST /v1/apps/{}/restart":                             TargetApp,
	"POST /v1/apps/{}/edge-rules/rollback":                 TargetApp,
	"POST /v1/apps/{}/edge-rules":                          TargetApp,
	"PATCH /v1/edge-rules/{}":                              TargetEdgeRule,
	"POST /v1/edge-rules/{}/waf-exclusions":                TargetEdgeRule,
	"PUT /v1/apps/{}/edge-protection/attack-mode":          TargetApp,
	"POST /v1/apps/{}/openapi/apply":                       TargetApp,
	"POST /v1/apps/{}/route-policy/apply":                  TargetApp,
	"POST /v1/apps/{}/routes/insights/{}/fix":              TargetApp,
	"PATCH /v1/deployments/{}/traffic":                     TargetDeployment,
	"POST /v1/deployments/{}/promote":                      TargetDeployment,
	"POST /v1/deployments/{}/promote-with-application-ack": TargetDeployment,
	"POST /v1/deployments/{}/canary/advance":               TargetDeployment,
	"POST /v1/deployments/{}/retry":                        TargetDeployment,
	"POST /v1/deployments/{}/approvals":                    TargetDeployment,
}

// credentials mint keys and tokens; every POST ending in /rotate or
// /rotate-secret is one too.
var credentials = map[string]bool{
	"POST /v1/keys":                                                            true,
	"POST /v1/orgs/{}/keys":                                                    true,
	"PATCH /v1/account/keys/grace_window_days":                                 true,
	"POST /v1/apps/{}/deploy-tokens":                                           true,
	"POST /v1/apps/{}/ai-access-tokens":                                        true,
	"POST /v1/apps/{}/issue-ingest-tokens":                                     true,
	"POST /v1/apps/{}/consumers/{}/keys":                                       true,
	"POST /v1/account/platform-tenants/{}/access-tokens":                       true,
	"POST /v1/apps/{}/realtime/endpoints/{}/channels/{}/groups/{}/credentials": true,
	"POST /v1/apps/{}/realtime/endpoints/{}/auth/rotate/finalize":              true,
}

// billing routes change plans, subscriptions, payment and spending settings.
var billing = map[string]bool{
	"PATCH /v1/account/plan":                                true,
	"PATCH /v1/orgs/{}/plan":                                true,
	"POST /v1/account/platform-tenants/{}/plan-assignments": true,
	"POST /v1/apps/{}/consumers/{}/plan-assignments":        true,
	"PUT /v1/apps/{}/consumer-billing-policy":               true,
}

var billingPath = regexp.MustCompile(`(^|/)(billing|consumer-billing)(/|$)|/billing-[a-z-]+(/|$)|^/v1/billing/`)

// Classify returns the rule for a request's route pattern, and false when
// the request needs no approval.
func Classify(pattern string) (Rule, bool) {
	key := Normalize(pattern)
	method, path, ok := strings.Cut(key, " ")
	if !ok || method == "GET" || method == "HEAD" || method == "OPTIONS" || exempt[key] || strings.HasPrefix(path, "/v1/admin/") {
		return Rule{}, false
	}
	for _, prefix := range exemptPrefixes {
		if strings.HasPrefix(path, prefix) {
			return Rule{}, false
		}
	}
	switch {
	case method == "DELETE" || destructivePosts[key]:
		return Rule{Category: api.AgentApprovalDelete}, true
	case billing[key] || (billingPath.MatchString(path) && !strings.HasSuffix(path, "/preview") && !strings.HasSuffix(path, "/portal")):
		return Rule{Category: api.AgentApprovalBilling}, true
	case credentials[key] || (method == "POST" && (strings.HasSuffix(path, "/rotate") || strings.HasSuffix(path, "/rotate-secret"))):
		return Rule{Category: api.AgentApprovalCredentials}, true
	}
	if target, ok := production[key]; ok {
		return Rule{Category: api.AgentApprovalProduction, Target: target}, true
	}
	return Rule{}, false
}

// Effective is the set of categories a key must have approved: its own set
// (every category when it chose none) plus its organization's floor.
func Effective(own []string, chosen bool, floor []string) map[string]bool {
	out := map[string]bool{}
	if !chosen {
		for _, category := range api.AgentApprovalCategories {
			out[category] = true
		}
	}
	for _, category := range own {
		out[category] = true
	}
	for _, category := range floor {
		out[category] = true
	}
	return out
}
