package main_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 831 — amendment 13: `edge-rules waf-review` reads the review with the
// rule and window filters and prints each group with its reason, the
// exclude command for a likely false positive and an example lookup.
func TestEdgeRulesWAFReviewCommand(t *testing.T) {
	main.ResetJSONOut(t)
	var out bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	f := main.AuthedFakeAPI(t, `{"since":"2026-10-17T12:00:00Z","scanned":12,"truncated":false,"groups":[
		{"rule_id":"r1","rule_name":"waf","rule_mode":"block","crs_rule_id":942190,"category":"sqli","path":"/graphql",
		 "events":12,"detected":0,"warned":0,"blocked":12,"clients":7,"top_client_percent":20,"scanner_percent":0,
		 "first_seen":"2026-10-20T12:00:00Z","last_seen":"2026-10-24T12:00:00Z","request_ids":["req-9"],
		 "known_false_positive":"GraphQL query in the URL","excluded":false,"assessment":"likely_false_positive",
		 "reason":"a documented false positive: GraphQL query in the URL"}]}`, http.StatusOK)
	if code := main.CmdEdgeRules([]string{"waf-review", "--app", "demo", "--rule", "r1", "--since", "24h"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if (*f.SawMethod()) != http.MethodGet || !strings.HasPrefix((*f.SawPath()), "/v1/apps/demo/edge-rules/waf-review") {
		t.Fatalf("request = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	for _, want := range []string{"likely_false_positive", "942190", "/graphql", "waf (block)",
		"a documented false positive: GraphQL query in the URL",
		"gregale edge-rules waf-exclude r1 --add 942190",
		"gregale edge-rules events --app demo --request-id req-9"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// adr: 831 — amendment 13: `edge-rules waf-exclude` posts the IDs to add and
// remove, prints the rule's exclusions from the stored action, and refuses a
// missing rule, no IDs or a malformed ID before any request.
func TestEdgeRulesWAFExcludeCommand(t *testing.T) {
	main.ResetJSONOut(t)
	var out bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	f := main.AuthedFakeAPI(t, `{"id":"r1","kind":"waf","action":{"kind":"waf","waf":{"mode":"block","exclude_rule_ids":[942100,942190]}}}`, http.StatusOK)
	if code := main.CmdEdgeRules([]string{"waf-exclude", "r1", "--add", "942190, 942100", "--remove", "920350"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var req api.EdgeWAFExclusionsRequest
	if (*f.SawMethod()) != http.MethodPost || (*f.SawPath()) != "/v1/edge-rules/r1/waf-exclusions" ||
		json.Unmarshal((*f.SawBody()), &req) != nil || len(req.Add) != 2 || len(req.Remove) != 1 || req.Remove[0] != 920350 {
		t.Fatalf("request = %s %s %s", (*f.SawMethod()), (*f.SawPath()), (*f.SawBody()))
	}
	if !strings.Contains(out.String(), "Rule r1 now excludes: 942100, 942190") {
		t.Fatalf("output = %q", out.String())
	}
	(*main.OsStdout) = io.Discard

	for name, args := range map[string][]string{
		"no rule":      {"waf-exclude", "--add", "942190"},
		"two rules":    {"waf-exclude", "r1", "r2", "--add", "942190"},
		"no ids":       {"waf-exclude", "r1"},
		"not a number": {"waf-exclude", "r1", "--add", "sqli"},
		"not CRS":      {"waf-exclude", "r1", "--add", "12"},
	} {
		f = main.AuthedFakeAPI(t, `{}`, http.StatusOK)
		if code := main.CmdEdgeRules(args); code == 0 || (*f.SawMethod()) != "" {
			t.Errorf("%s: exit %d, request %q; want failure before any request", name, code, (*f.SawMethod()))
		}
	}
}
