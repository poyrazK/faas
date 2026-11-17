package agentapproval

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		pattern  string
		category string
		target   Target
	}{
		{"DELETE /v1/apps/{slug}", api.AgentApprovalDelete, TargetNone},
		{"DELETE /v1/postgres/databases/{id}", api.AgentApprovalDelete, TargetNone},
		{"DELETE /v1/apps/{slug}/buckets/{bucket}", api.AgentApprovalDelete, TargetNone},
		{"POST /v1/preview/{slug}/destroy", api.AgentApprovalDelete, TargetNone},
		{"POST /v1/apps/{slug}/deployments", api.AgentApprovalProduction, TargetApp},
		{"POST /v1/apps/{name}/rollback", api.AgentApprovalProduction, TargetApp},
		{"PATCH /v1/deployments/{deployment}/traffic", api.AgentApprovalProduction, TargetDeployment},
		{"POST /v1/deployments/{id}/approvals", api.AgentApprovalProduction, TargetDeployment},
		// adr: 091 — amendment 2026-11-07: edge-rule writes move or gate traffic.
		{"POST /v1/apps/{slug}/edge-rules", api.AgentApprovalProduction, TargetApp},
		{"PATCH /v1/edge-rules/{id}", api.AgentApprovalProduction, TargetEdgeRule},
		{"POST /v1/edge-rules/{id}/waf-exclusions", api.AgentApprovalProduction, TargetEdgeRule},
		{"PUT /v1/apps/{slug}/edge-protection/attack-mode", api.AgentApprovalProduction, TargetApp},
		{"POST /v1/apps/{slug}/openapi/apply", api.AgentApprovalProduction, TargetApp},
		{"POST /v1/apps/{slug}/route-policy/apply", api.AgentApprovalProduction, TargetApp},
		{"POST /v1/apps/{slug}/routes/insights/{id}/fix", api.AgentApprovalProduction, TargetApp},
		{"DELETE /v1/edge-rules/{id}", api.AgentApprovalDelete, TargetNone},
		// Deploy review 2026-11-10 (D3): project deploys, promotions,
		// release sets and GitOps sources, in their ServeMux spelling.
		// Apps/projects review 2026-11-10 (P10): production configuration
		// reaches traffic at the next wake, like a restart.
		{"PUT /v1/apps/{slug}/env/{key}", api.AgentApprovalProduction, TargetAppConfig},
		{"POST /v1/apps/{slug}/env/batch", api.AgentApprovalProduction, TargetAppConfig},
		{"PUT /v1/apps/{slug}/secrets/{key}", api.AgentApprovalProduction, TargetAppConfig},
		{"PUT /v1/apps/{slug}/secret-references/{key}", api.AgentApprovalProduction, TargetAppConfig},
		{"PATCH /v1/apps/{slug}", api.AgentApprovalProduction, TargetApp},
		{"PUT /v1/projects/{slug}/environments/{environment}/config", api.AgentApprovalProduction, TargetNone},
		{"PUT /v1/projects/{slug}/environments/{environment}/secrets/{key}", api.AgentApprovalProduction, TargetNone},
		{"DELETE /v1/apps/{slug}/env/{key}", api.AgentApprovalDelete, TargetNone},
		{"POST /v1/projects/apply-plan", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/projects/safe-deploy", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/orgs/{organization}/projects/{slug}/deploy", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/projects/{slug}/environments/{environment}/promote", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promote", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/projects/{slug}/environments/{environment}/promotions/{promotion}/{action}", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/projects/{slug}/environments/{environment}/promotions/{promotion}/rollback", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/projects/{slug}/environments/{environment}/release-sets/activate", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/projects/{slug}/environments/{environment}/release-sets/rollback", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets", api.AgentApprovalProduction, TargetNone},
		{"PATCH /v1/projects/{slug}/environments/{environment}/gitops/source", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/projects/{slug}/environments/{environment}/gitops/revisions/approve", api.AgentApprovalProduction, TargetNone},
		{"POST /v1/keys", api.AgentApprovalCredentials, TargetNone},
		{"POST /v1/keys/{id}/rotate", api.AgentApprovalCredentials, TargetNone},
		{"POST /v1/apps/{slug}/secrets/{key}/rotate", api.AgentApprovalCredentials, TargetNone},
		{"POST /v1/apps/{slug}/webhooks/{id}/rotate-secret", api.AgentApprovalCredentials, TargetNone},
		{"POST /v1/apps/{slug}/deploy-tokens", api.AgentApprovalCredentials, TargetNone},
		{"PATCH /v1/account/plan", api.AgentApprovalBilling, TargetNone},
		{"PATCH /v1/orgs/{slug}/plan", api.AgentApprovalBilling, TargetNone},
		{"POST /v1/billing/cancel", api.AgentApprovalBilling, TargetNone},
		{"PUT /v1/orgs/{slug}/billing/email", api.AgentApprovalBilling, TargetNone},
		{"PUT /v1/account/billing-connections/{provider}", api.AgentApprovalBilling, TargetNone},
	}
	for _, c := range cases {
		rule, ok := Classify(c.pattern)
		if !ok || rule.Category != c.category || rule.Target != c.target {
			t.Errorf("Classify(%q) = %+v, %v; want %s/%d", c.pattern, rule, ok, c.category, c.target)
		}
	}
	for _, pattern := range []string{
		"GET /v1/apps/{slug}",
		"POST /v1/apps",
		"DELETE /v1/agent-connections/self",
		"DELETE /v1/uploads/{id}",
		"DELETE /v1/dev/sessions/{project}",
		"DELETE /v1/deployments/{id}/schedule",
		"POST /v1/billing/budgets/preview",
		"POST /v1/consumer-billing/{slug}/portal",
		"POST /v1/apps/{slug}/route-policy/plan",
		"POST /v1/projects/{slug}/environments/{environment}/promotion-preview",
		"POST /v1/projects/{slug}/environments/{environment}/gitops/plan",
		"POST /v1/projects/{slug}/environments/{environment}/release-sets/prepare",
		"DELETE /v1/admin/accounts/{id}",
	} {
		if rule, ok := Classify(pattern); ok {
			t.Errorf("Classify(%q) = %+v; want no approval", pattern, rule)
		}
	}
}

func TestEffective(t *testing.T) {
	all := Effective(nil, false, nil)
	if len(all) != len(api.AgentApprovalCategories) {
		t.Fatalf("a key that chose nothing = %v; want every category", all)
	}
	none := Effective([]string{}, true, nil)
	if len(none) != 0 {
		t.Fatalf("a key that chose none = %v", none)
	}
	floor := Effective([]string{}, true, []string{api.AgentApprovalBilling})
	if !floor[api.AgentApprovalBilling] || len(floor) != 1 {
		t.Fatalf("the organization's floor = %v", floor)
	}
}

// specMutations reads every mutating operation of the public OpenAPI spec.
func specMutations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	pathLine := regexp.MustCompile(`^  (/v1/\S+):\s*$`)
	methodLine := regexp.MustCompile(`^    (post|put|patch|delete):`)
	var current string
	var ops []string
	for _, line := range strings.Split(string(raw), "\n") {
		if m := pathLine.FindStringSubmatch(line); m != nil {
			current = m[1]
			continue
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			current = ""
		}
		if m := methodLine.FindStringSubmatch(line); m != nil && current != "" {
			ops = append(ops, Normalize(strings.ToUpper(m[1])+" "+current))
		}
	}
	if len(ops) < 500 {
		t.Fatalf("found only %d mutating operations in the spec; the parser is broken", len(ops))
	}
	return ops
}

// Every listed route must exist, so a renamed route cannot silently fall out
// of a category; every DELETE outside the exemptions needs approval.
// ADR-20261111150000: the debugger tunnel is a read that needs approval, and
// one approval covers it for a window; other reads stay ungated.
func TestClassifyGatedReads(t *testing.T) {
	rule, ok := Classify("GET /v1/apps/{slug}/debug")
	if !ok || rule.Category != api.AgentApprovalCredentials || rule.Target != TargetNone || rule.Window != api.DevDebugApprovalWindow {
		t.Fatalf("Classify(debug tunnel) = %+v, %v; want credentials with the debugger window", rule, ok)
	}
	if !RecordedRead("GET /v1/apps/{name}/debug") {
		t.Error("the debugger tunnel must be written to the agent activity log")
	}
	for _, pattern := range []string{"GET /v1/apps/{slug}", "GET /v1/apps/{slug}/logs", "POST /v1/keys"} {
		if RecordedRead(pattern) {
			t.Errorf("RecordedRead(%q) = true; want false", pattern)
		}
	}
	if rule, _ := Classify("POST /v1/keys"); rule.Window != 0 {
		t.Errorf("Classify(POST /v1/keys).Window = %v; other approvals stay single-use", rule.Window)
	}
}

func TestClassificationCoversTheSpec(t *testing.T) {
	ops := map[string]bool{}
	for _, op := range specMutations(t) {
		ops[op] = true
	}
	for _, table := range []map[string]bool{exempt, destructivePosts, credentials, billing} {
		for pattern := range table {
			if !ops[pattern] {
				t.Errorf("%s is listed but is not an operation of api/openapi.yaml", pattern)
			}
		}
	}
	for pattern := range production {
		if !ops[pattern] {
			t.Errorf("%s is listed but is not an operation of api/openapi.yaml", pattern)
		}
	}
	// A route-only entry the spec does carry belongs in production, where
	// this test guards it against renames.
	for pattern := range productionRouteOnly {
		if ops[pattern] {
			t.Errorf("%s is an operation of api/openapi.yaml; list it in production", pattern)
		}
	}
	for op := range ops {
		method, path, _ := strings.Cut(op, " ")
		if method != "DELETE" || exempt[op] || strings.HasPrefix(path, "/v1/admin/") || strings.HasPrefix(path, "/v1/dev/") {
			continue
		}
		if rule, ok := Classify(op); !ok || rule.Category != api.AgentApprovalDelete {
			t.Errorf("%s = %+v, %v; every DELETE needs approval", op, rule, ok)
		}
	}
}
