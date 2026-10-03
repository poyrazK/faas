package routerequirements

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

func coverageConfig(t *testing.T, body string) PreviewConfig {
	t.Helper()
	config, err := ParsePreview([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return config
}

const coverageConsumerConfig = `{"version":2,"groups":[{"name":"api","path_prefix":"/api/","methods":["POST"],"require":{"authentication":"consumer"}}]}`

func coverageInventory(paths ...string) CoverageInventory {
	inventory := CoverageInventory{Status: "available", Source: "captured_candidate_contract", Deployment: "candidate", SHA256: "hash"}
	for _, path := range paths {
		inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: path})
	}
	return inventory
}

func TestCoverageConfigStrictnessAndLegacyIsolation(t *testing.T) {
	config := coverageConfig(t, strings.ReplaceAll(coverageConsumerConfig, `"POST"`, `"post"`))
	if config.Groups[0].Methods[0] != "POST" {
		t.Fatal(config)
	}
	if _, err := Parse([]byte(coverageConsumerConfig)); err == nil {
		t.Fatal("server/planner v1 accepted route groups")
	}
	for _, body := range []string{
		`{"version":2}`, `{"version":3,"groups":[]}`,
		strings.ReplaceAll(coverageConsumerConfig, "path_prefix", "path_prefx"),
		strings.ReplaceAll(coverageConsumerConfig, "/api/", "/api"),
		strings.ReplaceAll(coverageConsumerConfig, "/api/", "/{tenant}/"),
		strings.ReplaceAll(coverageConsumerConfig, "/api/", "/a//b/"),
		strings.ReplaceAll(coverageConsumerConfig, "/api/", "/api/*/"),
		strings.ReplaceAll(coverageConsumerConfig, `"name":"api"`, `"name":""`),
		strings.ReplaceAll(coverageConsumerConfig, `["POST"]`, `["POST","post"]`),
		strings.ReplaceAll(coverageConsumerConfig, `["POST"]`, `[]`),
		strings.ReplaceAll(coverageConsumerConfig, `["POST"]`, `["TRACE"]`),
		strings.ReplaceAll(coverageConsumerConfig, `"consumer"`, `"magic"`),
		`{"version":1,"groups":[{"name":"api"}]}`,
		`{"version":2,"public":[{"method":"GET","path":"/health","reason":""}]}`,
		`{"version":2,"routes":[{"method":"GET","path":"/api//x","require":{"authentication":"consumer"}}]}`,
		`{"version":2,"public":[{"method":"GET","path":"/health","reason":"ok"},{"method":"get","path":"/health","reason":"duplicate"}]}`,
		`{"version":2,"routes":[{"method":"GET","path":"/health","require":{"authentication":"consumer"}}],"public":[{"method":"GET","path":"/health","reason":"conflict"}]}`,
		"version: 2\nversion: 2\npublic: []",
		coverageConsumerConfig + "\n---\nversion: 2",
	} {
		if _, err := ParsePreview([]byte(body)); err == nil {
			t.Fatalf("accepted invalid config %s", body)
		}
	}
	if _, err := ParsePreview([]byte(strings.Repeat(" ", api.RouteRequirementsMaxBytes+1))); err == nil {
		t.Fatal("unbounded input")
	}
	groups := make([]RouteGroup, api.RouteCoverageMaxGroups+1)
	for i := range groups {
		groups[i] = RouteGroup{Name: fmt.Sprint(i), PathPrefix: "/", Methods: []string{"GET"}, Require: Checks{Authentication: "consumer"}}
	}
	body, _ := json.Marshal(PreviewConfig{Version: 2, Groups: groups})
	if _, err := ParsePreview(body); err == nil {
		t.Fatal("unbounded groups")
	}
}

func TestCoverageAssignmentsAreConjunctiveAndExceptionsAreExact(t *testing.T) {
	config := coverageConfig(t, `{"version":2,"groups":[{"name":"identity","path_prefix":"/api/","methods":["POST"],"require":{"authentication":"consumer"}},{"name":"limit","path_prefix":"/api/","methods":["POST"],"require":{"throttle":{"key_by":"consumer_id"}}}],"public":[{"method":"POST","path":"/api/health","reason":"secret-exception-rationale"}]}`)
	context := requirementContext()
	inventory := coverageInventory("/api/orders/{id}", "/api/health", "/other")
	beforeConfig, _ := json.Marshal(config)
	beforeContext, _ := json.Marshal(context)
	beforeRoutes := append([]CapturedRoute(nil), inventory.Routes...)
	report := EvaluatePreview(config, "digest", context, inventory)
	if report.Status != "violated" || report.Groups[0].Status != "satisfied" || report.Groups[1].Status != "violated" || report.Groups[1].ExemptRoutes != 1 {
		t.Fatal(report)
	}
	if report.Routes[0].Status != "violated" || len(report.Routes[0].Checks) != 2 || report.Routes[1].Checks[0].Code != "public_exception" || report.Routes[2].Checks[0].Code != "route_uncovered" {
		t.Fatal(report.Routes)
	}
	body, _ := json.Marshal(report)
	if strings.Contains(string(body), "secret-") {
		t.Fatal("public rationale leaked")
	}
	afterConfig, _ := json.Marshal(config)
	afterContext, _ := json.Marshal(context)
	if string(beforeConfig) != string(afterConfig) || string(beforeContext) != string(afterContext) || !reflect.DeepEqual(beforeRoutes, inventory.Routes) {
		t.Fatal("input mutated")
	}
	// An exact requirement for one sample is not an assignment for a template.
	config = coverageConfig(t, `{"version":2,"routes":[{"method":"POST","path":"/api/orders/42","require":{"authentication":"consumer"}}]}`)
	report = EvaluatePreview(config, "digest", context, coverageInventory("/api/orders/{id}"))
	if report.Routes[0].Checks[0].Code != "route_uncovered" || report.Routes[1].Checks[0].Code != "assignment_not_in_candidate" {
		t.Fatal(report)
	}
	config = coverageConfig(t, coverageConsumerConfig)
	report = EvaluatePreview(config, "digest", context, coverageInventory("/other"))
	if report.Groups[0].Code != "group_no_routes" {
		t.Fatal("empty group silently passed", report)
	}
	// Prefixes exclude the root and neighboring prefixes, and methods are explicit.
	inventory = coverageInventory("/api", "/apix/x", "/api/x")
	inventory.Routes[2].Method = "GET"
	report = EvaluatePreview(config, "digest", context, inventory)
	for _, row := range report.Routes {
		if row.Checks[0].Code != "route_uncovered" {
			t.Fatal(row)
		}
	}
}

func TestCoverageFamilyPolicyPrecedenceAndMutations(t *testing.T) {
	config := coverageConfig(t, `{"version":2,"groups":[{"name":"api","path_prefix":"/api/","methods":["POST"],"require":{"throttle":{"key_by":"consumer_id"}}}]}`)
	for _, test := range []struct {
		name, status, code string
		mutate             func(*Context)
	}{
		{"nested full coverage", "satisfied", "throttle_matches", func(*Context) {}},
		{"exact sample only", "unknown", "policy_varies_with_path", func(c *Context) { c.Rules[0].MatchPath = "/api/orders/x/events/y" }},
		{"higher partial", "unknown", "policy_varies_with_path", func(c *Context) {
			r := requirementRule("partial", "throttle", sharedThrottle, 1)
			r.MatchPath = "/api/orders/42/*"
			c.Rules = append(c.Rules, r)
		}},
		{"lower partial", "satisfied", "throttle_matches", func(c *Context) {
			r := requirementRule("partial", "throttle", sharedThrottle, 20)
			r.MatchPath = "/api/orders/42/*"
			c.Rules = append(c.Rules, r)
		}},
		{"tie", "unknown", "equal_priority_candidates", func(c *Context) { c.Rules = append(c.Rules, requirementRule("other", "throttle", sharedThrottle, 10)) }},
		{"conditional", "unknown", "header_dependent_rule", func(c *Context) { c.Rules[0].MatchHeaders = map[string]string{"Authorization": "secret-selector"} }},
		{"disjoint", "violated", "missing_throttle_rule", func(c *Context) { c.Rules[0].MatchPath = "/admin/*" }},
		{"complex glob", "unknown", "policy_varies_with_path", func(c *Context) { c.Rules[0].MatchPath = "/api/orders/[ab]/*" }},
		{"disjoint complex glob", "violated", "missing_throttle_rule", func(c *Context) { c.Rules[0].MatchPath = "/admin/[ab]/*" }},
		{"invalid glob", "unknown", "invalid_path_selector", func(c *Context) { c.Rules[0].MatchPath = "[" }},
		{"partial rewrite", "unknown", "policy_varies_with_path", func(c *Context) {
			r := requirementRule("rewrite", "rewrite", `{"rewrite":{"path":"/secret-target"}}`, 1)
			r.MatchPath = "/api/orders/42/*"
			c.Rules = append(c.Rules, r)
		}},
		{"full route", "unknown", "request_context_mutation", func(c *Context) {
			c.Rules = append(c.Rules, requirementRule("route", "route", `{"route":{"app_slug":"secret-app"}}`, 1))
		}},
		{"response headers", "satisfied", "throttle_matches", func(c *Context) {
			c.Rules = append(c.Rules, requirementRule("headers", "headers", `{"headers":{"response_headers":[{"op":"set","name":"X-Mode","value":"secret-value"}]}}`, 1))
		}},
		{"request headers", "unknown", "request_context_mutation", func(c *Context) {
			c.Rules = append(c.Rules, requirementRule("headers", "headers", `{"headers":{"request_headers":[{"op":"set","name":"X-Mode","value":"secret-value"}]}}`, 1))
		}},
		{"invalid action", "unknown", "invalid_rule_action", func(c *Context) { c.Rules[0].Action = json.RawMessage(`{"throttle":null}`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			context := requirementContext()
			context.Rules = []api.EdgeRuleResponse{requirementRule("limit", "throttle", consumerThrottle, 10)}
			test.mutate(&context)
			report := EvaluatePreview(config, "digest", context, coverageInventory("/api/orders/{id}/events/{event}"))
			if report.Status != test.status || report.Routes[0].Checks[0].Code != test.code {
				t.Fatal(report)
			}
			body, _ := json.Marshal(report)
			if strings.Contains(string(body), "secret-") {
				t.Fatal("rule metadata leaked")
			}
		})
	}
}

func TestCoverageAuthenticationBudgetAndUnsupportedFamilies(t *testing.T) {
	config := coverageConfig(t, `{"version":2,"groups":[{"name":"api","path_prefix":"/api/","methods":["POST"],"require":{"authentication":"jwt","budget":{"explicit":true,"max_ms":2000}}}]}`)
	context := requirementContext()
	context.Rules = []api.EdgeRuleResponse{requirementRule("jwt", "jwt", `{"jwt":{"issuer":"secret-issuer","jwks_url":"https://example.com/secret-jwks","algorithms":["RS256"]}}`, 1)}
	report := EvaluatePreview(config, "digest", context, coverageInventory("/api/{id}"))
	if report.Status != "satisfied" || len(report.Routes[0].Checks) != 2 {
		t.Fatal(report)
	}
	for _, path := range []string{"/api/{id:path}", "/api/item-{id}", "/api//{id}", "/api/%61", "/api/../{id}"} {
		report = EvaluatePreview(config, "digest", context, coverageInventory(path))
		if report.Status != "unknown" || report.Routes[0].Checks[0].Code != "unsupported_path_family" {
			t.Fatal(path, report)
		}
	}
	config = coverageConfig(t, coverageConsumerConfig)
	if report = EvaluatePreview(config, "digest", context, coverageInventory("/{tenant}/x")); report.Status != "unknown" || report.Routes[0].Checks[0].Code != "group_membership_varies_with_path" {
		t.Fatal(report)
	}
	context.Unavailable = "rules:read_failed"
	if report = EvaluatePreview(config, "digest", context, coverageInventory("/api/{id}")); report.Status != "unknown" {
		t.Fatal(report)
	}
	legacy := PreviewConfig{Version: 1, Routes: requirementConfig(t, `{"authentication":"consumer"}`).Routes}
	context = requirementContext()
	if report = EvaluatePreview(legacy, "digest", context, CoverageInventory{}); report.Status != "satisfied" || report.Version != 1 || report.Coverage != nil {
		t.Fatal("v1 acquired inventory dependency", report)
	}
}

// Compare proofs against the gateway over representatives of every literal
// equality class, plus other nonempty values and nested descendants.
func TestCoverageSelectorProofAgreesWithGateway(t *testing.T) {
	patterns := []string{"", "*", "/*", "/api/*", "/api/*/events/*", "/api/x", "/api/x/events/*", "/other/*", "/api/*/events/y", "/api/*/events", "/api/*/events/", "/api/*/*/*", "/api/x/"}
	for _, route := range []string{"/api/{id}", "/api/{id}/events/{event}", "/{root}/{id}", "/api/{id}/events/", "/api/literal"} {
		family, code := parseFamily(route)
		if code != "" {
			t.Fatal(code)
		}
		for _, pattern := range patterns {
			all, anyMatch := true, false
			for _, first := range []string{"api", "other", "x", "y", "events", "literal", "unlisted"} {
				for _, second := range []string{"api", "other", "x", "y", "events", "literal", "unlisted"} {
					values := []string{first, second}
					index := 0
					segments := append([]string(nil), family.segments...)
					for i, parameter := range family.parameters {
						if parameter {
							segments[i] = values[index]
							index++
						}
					}
					matched, err := api.MatchEdgeRulePath(pattern, "/"+strings.Join(segments, "/"))
					if err != nil {
						t.Fatal(err)
					}
					all, anyMatch = all && matched, anyMatch || matched
				}
			}
			want := pathPartial
			if all {
				want = pathAll
			} else if !anyMatch {
				want = pathNone
			}
			if got := selectorRelation(pattern, family); got != want {
				t.Fatalf("%s against %s: got %d want %d", pattern, route, got, want)
			}
		}
	}
}

func TestCoverageBoundsDiscardPartialResults(t *testing.T) {
	config := coverageConfig(t, coverageConsumerConfig)
	context := requirementContext()
	for _, count := range []int{api.RouteCoverageMaxInventoryRoutes + 1, api.RouteCoverageMaxRules + 1} {
		inventory := coverageInventory("/api/{id}")
		if count == api.RouteCoverageMaxInventoryRoutes+1 {
			inventory.Routes = make([]CapturedRoute, count)
		} else {
			context.Rules = make([]api.EdgeRuleResponse, count)
		}
		report := EvaluatePreview(config, "digest", context, inventory)
		if report.Status != "unknown" || report.Coverage.Code != "coverage_limit_exceeded" || len(report.Routes) != 0 || report.Groups[0].Status != "unknown" {
			t.Fatal(report)
		}
	}
	context = requirementContext()
	context.Rules = []api.EdgeRuleResponse{requirementRule("oversize", "jwt", strings.Repeat("x", api.RouteCoverageMaxWorkBytes+1), 1)}
	if report := EvaluatePreview(config, "digest", context, coverageInventory("/api/{id}")); report.Coverage.Code != "coverage_limit_exceeded" || len(report.Routes) != 0 {
		t.Fatal(report)
	}
	// Output exhaustion occurs after some successful rows, and still discards all.
	groups := []RouteGroup{}
	for i := range 10 {
		groups = append(groups, RouteGroup{Name: fmt.Sprint(i), PathPrefix: "/", Methods: []string{"POST"}, Require: Checks{Authentication: "application"}})
	}
	config = PreviewConfig{Version: 2, Groups: groups}
	inventory := coverageInventory()
	for i := range 1100 {
		inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: fmt.Sprintf("/r/%d", i)})
	}
	if report := EvaluatePreview(config, "digest", requirementContext(), inventory); report.Coverage.Code != "coverage_limit_exceeded" || len(report.Routes) != 0 {
		t.Fatal(report)
	}
	// Rule visits are bounded even when every rule is irrelevant to the
	// requested policy kind, and exhaustion discards earlier successful rows.
	context = requirementContext()
	for i := range api.RouteCoverageMaxRules {
		context.Rules = append(context.Rules, requirementRule(fmt.Sprint(i), "ip", `{}`, 1))
	}
	config = coverageConfig(t, coverageConsumerConfig)
	inventory = coverageInventory()
	for i := range api.RouteCoverageMaxInventoryRoutes {
		inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: fmt.Sprintf("/api/%d", i)})
	}
	if report := EvaluatePreview(config, "digest", context, inventory); report.Coverage.Code != "coverage_limit_exceeded" || len(report.Routes) != 0 {
		t.Fatal(report)
	}
}

func TestCoverageCandidateInventoryIsCompleteOrUnavailable(t *testing.T) {
	for _, test := range []struct {
		paths, status, code string
		count               int
	}{
		{`{"/api/{id}":{"post":{"responses":{}}},"/health":{"get":{"responses":{}}}}`, "available", "", 2},
		{`{}`, "unavailable", "candidate_inventory_empty", 0},
		{`{"/api":{"$ref":"#/components/pathItems/private"}}`, "unavailable", "candidate_inventory_referenced", 0},
		{`{"/api":{"trace":{"responses":{}}}}`, "unavailable", "candidate_inventory_incomplete", 0},
		{`{"/api":{"get":null}}`, "unavailable", "candidate_inventory_incomplete", 0},
		{`{"/api":{"POST":{"responses":{}}}}`, "unavailable", "candidate_inventory_incomplete", 0},
		{`{"/api":{"postt":{"responses":{}}}}`, "unavailable", "candidate_inventory_incomplete", 0},
		{`{"/api":{"get":{"$ref":"#/private"}}}`, "unavailable", "candidate_inventory_referenced", 0},
		{`{"/api/{id}":{"get":{"responses":{}}},"/api/{name}":{"post":{"responses":{}}}}`, "unavailable", "candidate_inventory_ambiguous", 0},
	} {
		spec, err := openapidiff.LoadBytes([]byte(`{"openapi":"3.1.0","info":{"title":"demo","version":"1"},"paths":` + test.paths + `}`))
		if err != nil {
			t.Fatal(err)
		}
		inventory := CandidateInventory(spec, "candidate", "hash")
		if inventory.Status != test.status || inventory.Code != test.code || len(inventory.Routes) != test.count || inventory.RouteCount != test.count {
			t.Fatal(test, inventory)
		}
	}
	if report := EvaluatePreview(coverageConfig(t, coverageConsumerConfig), "digest", requirementContext(), CandidateInventory(nil, "candidate", "")); report.Status != "unknown" || len(report.Routes) != 0 {
		t.Fatal(report)
	}
}

func TestCoverageServerPathsCannotBeSilentlyIgnored(t *testing.T) {
	for _, test := range []struct{ root, path, operation, code string }{
		{``, ``, ``, ``},
		{`"servers":[],`, ``, ``, ``},
		{`"servers":[{"url":"/"}],`, ``, ``, ``},
		{`"servers":[{"url":"https://example.com/"}],`, ``, ``, ``},
		{`"servers":[{"url":"/secret-v1"}],`, ``, ``, `candidate_server_path_unavailable`},
		{``, `"servers":[{"url":"/secret-v1"}],`, ``, `candidate_server_path_unavailable`},
		{``, ``, `"servers":[{"url":"/secret-v1"}],`, `candidate_server_path_unavailable`},
		{`"servers":[{"url":"/secret-v1"}],`, `"servers":[{"url":"/"}],`, ``, ``},
		{`"servers":[{"url":"/secret-v1"}],`, ``, `"servers":[{"url":"/"}],`, ``},
		{`"servers":[{"url":"/"}],`, `"servers":[{"url":"/secret-v1"}],`, `"servers":[{"url":"/"}],`, ``},
		{`"servers":[{"url":"https://example.com/secret-v1"}],`, ``, ``, `candidate_server_path_unavailable`},
		{`"servers":[{"url":"secret-v1"}],`, ``, ``, `candidate_server_path_unavailable`},
		{`"servers":[{"url":"/{secret-variable}"}],`, ``, ``, `candidate_server_path_unavailable`},
		{`"servers":[{"url":"https://example.com/?secret=1"}],`, ``, ``, `candidate_server_path_unavailable`},
		{`"servers":[{"url":"https://secret-user@example.com/"}],`, ``, ``, `candidate_server_path_unavailable`},
		{`"servers":null,`, ``, ``, `candidate_server_path_unavailable`},
	} {
		spec, err := openapidiff.LoadBytes([]byte(`{"openapi":"3.1.0","info":{"title":"demo","version":"1"},` + test.root + `"paths":{"/api/{id}":{` + test.path + `"post":{` + test.operation + `"responses":{}}}}}`))
		if err != nil {
			t.Fatal(err)
		}
		inventory := CandidateInventory(spec, "candidate", "hash")
		if inventory.Code != test.code || (inventory.Status == "available") != (test.code == "") {
			t.Fatal(test, inventory)
		}
		result := EvaluatePreview(coverageConfig(t, coverageConsumerConfig), "digest", requirementContext(), inventory)
		body, _ := json.Marshal(result)
		if strings.Contains(string(body), "secret-") {
			t.Fatal("server metadata leaked")
		}
	}
}
