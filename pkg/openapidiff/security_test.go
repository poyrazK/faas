package openapidiff

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func securityTestSpec(t *testing.T, requirements any, schemes map[string]any) *Spec {
	t.Helper()
	spec := requestTestSpec(t, "3.1.0", map[string]any{"responses": map[string]any{}}, nil, nil)
	if requirements != nil {
		spec.Raw["security"] = requirements
	}
	if schemes != nil {
		spec.Raw["components"] = map[string]any{"securitySchemes": schemes}
	}
	return spec
}
func securityTestRequirements(names ...string) []any {
	clause := map[string]any{}
	for _, name := range names {
		clause[name] = []any{}
	}
	return []any{clause}
}
func securityTestSchemes() map[string]any {
	return map[string]any{
		"token": map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "private-format"},
		"key":   map[string]any{"type": "apiKey", "in": "header", "name": "private-key-header"},
		"oauth": map[string]any{"type": "oauth2", "flows": map[string]any{"clientCredentials": map[string]any{"tokenUrl": "https://private.example/token?secret=private-query", "scopes": map[string]any{"private-read": "private-description", "private-write": "private-description"}}}},
	}
}
func securityTestScopes(scopes ...string) []any {
	values := []any{}
	for _, scope := range scopes {
		values = append(values, scope)
	}
	return []any{map[string]any{"oauth": values}}
}
func securityTestFinding(row SecurityRoute, code string) bool {
	for _, finding := range row.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func TestCompareSecurityRequirements(t *testing.T) {
	a, b := securityTestRequirements("token"), securityTestRequirements("key")
	for _, test := range []struct {
		name          string
		before, after any
		status, code  string
		changed       bool
	}{
		{"absent", nil, nil, "unchanged", "", false},
		{"empty override equivalent", nil, []any{}, "unchanged", "", false},
		{"anonymous equivalent", []any{}, []any{map[string]any{}}, "unchanged", "", false},
		{"same credentials", a, a, "unchanged", "", false},
		{"anonymous added", a, []any{a[0], map[string]any{}}, "regression", "anonymous_access_added", true},
		{"requirements removed", a, []any{}, "regression", "anonymous_access_added", true},
		{"declaration removed", a, nil, "regression", "anonymous_access_added", true},
		{"authentication added", nil, a, "client_breaking", "authentication_required", true},
		{"optional tightened", []any{a[0], map[string]any{}}, a, "client_breaking", "authentication_required", true},
		{"AND weakened", securityTestRequirements("token", "key"), a, "regression", "security_requirements_weakened", true},
		{"AND tightened", a, securityTestRequirements("token", "key"), "client_breaking", "security_requirements_restricted", true},
		{"OR added", a, []any{a[0], b[0]}, "regression", "security_requirements_weakened", true},
		{"OR removed", []any{a[0], b[0]}, a, "client_breaking", "security_requirements_restricted", true},
		{"OR reordered", []any{a[0], b[0]}, []any{b[0], a[0]}, "unchanged", "", false},
		{"redundant AND alternative", a, []any{a[0], securityTestRequirements("token", "key")[0]}, "unchanged", "", false},
		{"duplicates", a, []any{a[0], a[0]}, "unchanged", "", false},
		{"scopes removed", securityTestScopes("private-read", "private-write"), securityTestScopes("private-read"), "regression", "security_requirements_weakened", true},
		{"scope added", securityTestScopes("private-read"), securityTestScopes("private-read", "private-write"), "client_breaking", "security_requirements_restricted", true},
		{"scopes reordered", securityTestScopes("private-read", "private-write"), securityTestScopes("private-write", "private-read", "private-read"), "unchanged", "", false},
		{"scope replaced", securityTestScopes("private-read"), securityTestScopes("private-write"), "unknown", "security_requirements_incomparable", true},
		{"mechanism replaced", a, b, "unknown", "security_requirements_incomparable", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := CompareSecurity(securityTestSpec(t, test.before, securityTestSchemes()), securityTestSpec(t, test.after, securityTestSchemes()))
			if err != nil || len(result.Routes) != 1 {
				t.Fatalf("comparison: %+v, %v", result, err)
			}
			row := result.Routes[0]
			if row.Status != test.status || row.Changed != test.changed || row.Complete != (test.status != "unknown") || (test.code != "" && !securityTestFinding(row, test.code)) || row.Regression != (test.status == "regression") || row.ClientBreaking != (test.status == "client_breaking") {
				t.Fatalf("got %+v", row)
			}
		})
	}
}

func TestCompareSecurityInheritanceAndDefinitions(t *testing.T) {
	before, after := securityTestSpec(t, securityTestRequirements("token"), securityTestSchemes()), securityTestSpec(t, []any{}, securityTestSchemes())
	for _, spec := range []*Spec{before, after} {
		spec.Paths["/items/{id}"].Methods["post"].Raw["security"] = []any{}
	}
	result, err := CompareSecurity(before, after)
	if err != nil || result.Routes[0].Changed || result.Routes[0].Baseline.Source != "operation" {
		t.Fatalf("ignored root: %+v, %v", result, err)
	}
	for _, test := range []struct {
		name     string
		old, new map[string]any
		status   string
	}{
		{"header case", map[string]any{"type": "apiKey", "in": "header", "name": "X-Key"}, map[string]any{"type": "apiKey", "in": "header", "name": "x-key"}, "unchanged"},
		{"header renamed", map[string]any{"type": "apiKey", "in": "header", "name": "X-Key"}, map[string]any{"type": "apiKey", "in": "header", "name": "X-Other"}, "unknown"},
		{"transport", map[string]any{"type": "apiKey", "in": "header", "name": "key"}, map[string]any{"type": "apiKey", "in": "query", "name": "key"}, "unknown"},
		{"query case", map[string]any{"type": "apiKey", "in": "query", "name": "Key"}, map[string]any{"type": "apiKey", "in": "query", "name": "key"}, "unknown"},
		{"http case and annotation", map[string]any{"type": "http", "scheme": "Bearer", "bearerFormat": "JWT"}, map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "other", "description": "private"}, "unchanged"},
		{"http mechanism", map[string]any{"type": "http", "scheme": "basic"}, map[string]any{"type": "http", "scheme": "bearer"}, "unknown"},
		{"OIDC provider", map[string]any{"type": "openIdConnect", "openIdConnectUrl": "https://a.example/.well-known/openid-configuration"}, map[string]any{"type": "openIdConnect", "openIdConnectUrl": "https://b.example/.well-known/openid-configuration"}, "unknown"},
		{"mutual TLS", map[string]any{"type": "mutualTLS"}, map[string]any{"type": "mutualTLS", "description": "private-ca"}, "unchanged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := CompareSecurity(securityTestSpec(t, securityTestRequirements("token"), map[string]any{"token": test.old}), securityTestSpec(t, securityTestRequirements("token"), map[string]any{"token": test.new}))
			if err != nil || result.Routes[0].Status != test.status || result.Routes[0].Changed != (test.status != "unchanged") {
				t.Fatalf("definition: %+v, %v", result, err)
			}
			if test.status == "unknown" && !securityTestFinding(result.Routes[0], "security_scheme_changed") {
				t.Fatalf("missing definition finding: %+v", result)
			}
		})
	}
}

func TestCompareSecurityInvalidEvidenceAndReferences(t *testing.T) {
	for _, test := range []struct {
		name        string
		requirement any
		scheme      any
		code        string
	}{
		{"non-list", "private-value", nil, "invalid_security_requirements"},
		{"invalid clause", []any{nil}, nil, "invalid_security_requirements"},
		{"missing definition", securityTestRequirements("token"), nil, "unresolved_security_scheme"},
		{"external ref", securityTestRequirements("token"), map[string]any{"$ref": "https://private.example/secret"}, "unresolved_security_scheme"},
		{"recursive ref", securityTestRequirements("token"), map[string]any{"$ref": "#/components/securitySchemes/token"}, "unresolved_security_scheme"},
		{"unsupported mechanism", securityTestRequirements("token"), map[string]any{"type": "http", "scheme": "private"}, "unsupported_security_scheme"},
		{"invalid location", securityTestRequirements("token"), map[string]any{"type": "apiKey", "in": "body", "name": "key"}, "invalid_security_scheme"},
		{"invalid scopes", []any{map[string]any{"token": "private-scope"}}, map[string]any{"type": "http", "scheme": "bearer"}, "invalid_security_requirements"},
		{"invalid role", []any{map[string]any{"token": []any{1}}}, map[string]any{"type": "http", "scheme": "bearer"}, "invalid_security_metadata"},
		{"invalid metadata", securityTestRequirements("token"), map[string]any{"type": "apiKey", "in": "header", "name": "private\nheader"}, "invalid_security_scheme"},
		{"invalid header token", securityTestRequirements("token"), map[string]any{"type": "apiKey", "in": "header", "name": "private header"}, "invalid_security_scheme"},
		{"relative OpenID provider", securityTestRequirements("token"), map[string]any{"type": "openIdConnect", "openIdConnectUrl": "/.well-known/openid-configuration"}, "relative_security_endpoint_not_compared"},
		{"missing provider host", securityTestRequirements("token"), map[string]any{"type": "openIdConnect", "openIdConnectUrl": "https://"}, "invalid_security_scheme"},
		{"relative OAuth endpoint", securityTestRequirements("token"), map[string]any{"type": "oauth2", "flows": map[string]any{"clientCredentials": map[string]any{"tokenUrl": "/token", "scopes": map[string]any{}}}}, "relative_security_endpoint_not_compared"},
		{"invalid OAuth", securityTestRequirements("token"), map[string]any{"type": "oauth2", "flows": map[string]any{}}, "invalid_oauth_flows"},
	} {
		t.Run(test.name, func(t *testing.T) {
			definitions := map[string]any{"token": test.scheme}
			if test.name == "missing definition" {
				definitions = nil
			}
			spec := securityTestSpec(t, test.requirement, definitions)
			result, err := CompareSecurity(spec, spec)
			if err != nil || result.Routes[0].Complete || result.Routes[0].Regression || result.Routes[0].ClientBreaking || !securityTestFinding(result.Routes[0], test.code) || result.Routes[0].Baseline.Authentication != "unknown" {
				t.Fatalf("invalid evidence inferred auth: %+v, %v", result, err)
			}
		})
	}
	before := securityTestSpec(t, securityTestRequirements("token"), securityTestSchemes())
	after := securityTestSpec(t, []any{map[string]any{}, nil}, nil)
	result, err := CompareSecurity(before, after)
	if err != nil || !result.Routes[0].Regression || result.Routes[0].Complete {
		t.Fatalf("known anonymous finding lost: %+v, %v", result, err)
	}
	definitionsWithChange := securityTestSchemes()
	definitionsWithChange["token"] = map[string]any{"type": "http", "scheme": "basic"}
	result, err = CompareSecurity(before, securityTestSpec(t, []any{securityTestRequirements("token")[0], map[string]any{}}, definitionsWithChange))
	if err != nil || !result.Routes[0].Regression || result.Routes[0].Complete || !securityTestFinding(result.Routes[0], "security_scheme_changed") {
		t.Fatalf("anonymous addition hid a credential definition change: %+v, %v", result, err)
	}
	before.Raw["security"] = nil
	result, err = CompareSecurity(before, securityTestSpec(t, nil, nil))
	if err != nil || result.Routes[0].Regression || result.Routes[0].Baseline.Authentication != "unknown" {
		t.Fatalf("null treated as absent: %+v, %v", result, err)
	}
	definitions := map[string]any{"token": map[string]any{"$ref": "#/components/securitySchemes/a~1b~0c", "type": "private-ignored-sibling"}, "a/b~c": map[string]any{"type": "http", "scheme": "bearer"}}
	result, err = CompareSecurity(securityTestSpec(t, securityTestRequirements("token"), definitions), securityTestSpec(t, securityTestRequirements("token"), securityTestSchemes()))
	if err != nil || result.Routes[0].Status != "unchanged" {
		t.Fatalf("local reference: %+v, %v", result, err)
	}
}

func TestCompareSecurityRolesVersionsAndOAuthScopes(t *testing.T) {
	before := securityTestSpec(t, []any{map[string]any{"token": []any{"private-role"}}}, securityTestSchemes())
	after := securityTestSpec(t, securityTestRequirements("token"), securityTestSchemes())
	result, err := CompareSecurity(before, after)
	if err != nil || !result.Routes[0].Regression {
		t.Fatalf("3.1 role restriction: %+v, %v", result, err)
	}
	before.version = "3.0.4"
	result, err = CompareSecurity(before, after)
	if err != nil || result.Routes[0].Complete || result.Routes[0].Regression {
		t.Fatalf("3.0 roles accepted: %+v, %v", result, err)
	}
	before = securityTestSpec(t, securityTestScopes("missing-private-scope"), securityTestSchemes())
	result, err = CompareSecurity(before, before)
	if err != nil || !securityTestFinding(result.Routes[0], "undeclared_oauth_scope") {
		t.Fatalf("missing scope: %+v, %v", result, err)
	}
}

func TestCompareSecurityPrivacyImmutabilityAndBounds(t *testing.T) {
	before := securityTestSpec(t, securityTestScopes("private-read", "private-write"), securityTestSchemes())
	after := securityTestSpec(t, securityTestScopes("private-read"), securityTestSchemes())
	beforeBytes, _ := json.Marshal(before.Raw)
	afterBytes, _ := json.Marshal(after.Raw)
	result, err := CompareSecurity(before, after)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "https:") {
		t.Fatalf("private metadata leaked: %s", encoded)
	}
	newBefore, _ := json.Marshal(before.Raw)
	newAfter, _ := json.Marshal(after.Raw)
	if !reflect.DeepEqual(beforeBytes, newBefore) || !reflect.DeepEqual(afterBytes, newAfter) {
		t.Fatal("input mutated")
	}
	after.Raw["security"] = make([]any, api.SecurityCompatibilityMaxNodes+1)
	if result, err := CompareSecurity(before, after); !errors.Is(err, ErrSecurityComparisonLimit) || len(result.Routes) != 0 {
		t.Fatalf("partial bounded comparison: %+v, %v", result, err)
	}
	if result, err := CompareSecurity(nil, before); err == nil || len(result.Routes) != 0 {
		t.Fatalf("nil evidence: %+v, %v", result, err)
	}
	after = securityTestSpec(t, nil, nil)
	after.Paths["/items/{id}"].Raw["trace"] = map[string]any{}
	if result, err := CompareSecurity(before, after); err == nil || len(result.Routes) != 0 {
		t.Fatalf("unparsed operation omitted: %+v, %v", result, err)
	}
	after.Paths["/items/{id}"].Raw["$ref"] = "#/components/pathItems/private"
	if _, err := CompareSecurity(before, after); err == nil {
		t.Fatal("referenced path item compared")
	}
	for _, limit := range []string{"depth", "routes", "findings", "bytes", "pairs"} {
		t.Run(limit, func(t *testing.T) {
			work := &securityWork{}
			switch limit {
			case "depth":
				spec := securityTestSpec(t, securityTestRequirements("token"), nil)
				schemes := map[string]any{"token": map[string]any{"$ref": "#/components/securitySchemes/0"}}
				for i := 0; i <= api.SecurityCompatibilityMaxDepth; i++ {
					schemes[fmt.Sprint(i)] = map[string]any{"$ref": "#/components/securitySchemes/" + fmt.Sprint(i+1)}
				}
				spec.Raw["components"] = map[string]any{"securitySchemes": schemes}
				if _, err := CompareSecurity(spec, spec); !errors.Is(err, ErrSecurityComparisonLimit) {
					t.Fatalf("depth limit: %v", err)
				}
				return
			case "routes":
				spec := securityTestSpec(t, nil, nil)
				for i := 0; i <= api.SecurityCompatibilityMaxRoutes; i++ {
					spec.Paths["/"+fmt.Sprint(i)] = spec.Paths["/items/{id}"]
				}
				if _, err := CompareSecurity(spec, spec); !errors.Is(err, ErrSecurityComparisonLimit) {
					t.Fatalf("route limit: %v", err)
				}
				return
			case "findings":
				work.findings = api.SecurityCompatibilityMaxFindings
				work.emit(&SecurityRoute{}, "unknown", "test", "")
			case "bytes":
				work.bytes = api.SecurityCompatibilityMaxWorkBytes
				work.metadata("x")
			case "pairs":
				work.nodes = api.SecurityCompatibilityMaxNodes
				work.covered([]securityClause{{}}, []securityClause{{}})
			}
			if !work.exceeded {
				t.Fatal("limit not charged")
			}
		})
	}
}

// Exhaustive truth-table oracle independent of the implication implementation.
// Each positive atom is a credential or a scope requirement. It checks all
// conjunctions and pairs of disjunctive alternatives over three atoms.
func TestSecurityImplicationTruthTable(t *testing.T) {
	clauses := []securityClause{}
	for mask := 0; mask < 8; mask++ {
		clause := securityClause{}
		if mask&1 != 0 {
			clause["a"] = map[string]bool{}
		}
		if mask&2 != 0 {
			clause["b"] = map[string]bool{}
		}
		if mask&4 != 0 {
			clause["a"] = map[string]bool{"scope": true}
		}
		clauses = append(clauses, clause)
	}
	policies := [][]securityClause{}
	for _, first := range clauses {
		for _, second := range clauses {
			policies = append(policies, []securityClause{first, second})
		}
	}
	accepts := func(policy []securityClause, credentialA, credentialB, scope bool) bool {
		for _, clause := range policy {
			_, needsA := clause["a"]
			_, needsB := clause["b"]
			if (!needsA || credentialA) && (!needsB || credentialB) && (!clause["a"]["scope"] || scope) {
				return true
			}
		}
		return false
	}
	for i, base := range policies {
		for j, candidate := range policies {
			want := true
			for values := 0; values < 8; values++ {
				a, b, s := values&1 != 0, values&2 != 0, values&4 != 0
				if accepts(base, a, b, s) && !accepts(candidate, a, b, s) {
					want = false
				}
			}
			work := &securityWork{}
			if got := work.covered(base, candidate); got != want {
				t.Fatalf("policies %d/%d: got %v want %v", i, j, got, want)
			}
		}
	}
}
