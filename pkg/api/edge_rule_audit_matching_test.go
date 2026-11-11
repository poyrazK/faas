package api_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEdgeRuleEncodedMethodOverrides(t *testing.T) {
	for _, query := range []string{"%5Fmethod=DELETE", "_m%65thod=DELETE", "_method=DELETE&other=%zz"} {
		if got := api.EdgeRuleMethodOverride("POST", http.Header{}, query); got != "DELETE" {
			t.Errorf("override for %q = %q", query, got)
		}
	}
}

func TestEdgeRuleRegexCaseFolding(t *testing.T) {
	for _, tc := range []struct {
		field, pattern string
		input          api.EdgeRuleMatchInput
	}{
		{"method", "^GET$", api.EdgeRuleMatchInput{Method: "GET"}},
		{"method", "^[A-Z]+$", api.EdgeRuleMatchInput{Method: "POST"}},
		{"country", "^US$", api.EdgeRuleMatchInput{Country: "US"}},
		{"host", `^API\.EXAMPLE\.COM$`, api.EdgeRuleMatchInput{Host: "api.example.com"}},
	} {
		program, err := api.CompileEdgeRuleMatch(&api.EdgeRuleMatchExpr{Field: tc.field, Op: "regex", Value: tc.pattern})
		if err != nil {
			t.Fatal(err)
		}
		if !program.Matches(tc.input) {
			t.Errorf("%s %q did not match", tc.field, tc.pattern)
		}
	}
	program, err := api.CompileEdgeRuleMatch(&api.EdgeRuleMatchExpr{Field: "header:x-value", Op: "regex", Value: "^UPPER$"})
	if err != nil {
		t.Fatal(err)
	}
	if program.Matches(api.EdgeRuleMatchInput{Headers: http.Header{"X-Value": {"upper"}}}) {
		t.Fatal("header values lost case sensitivity")
	}
}

func TestEdgeRulePathGlobValidation(t *testing.T) {
	for _, pattern := range []string{"/admin/[", "/admin/\\", "/admin/*/["} {
		if api.ValidateEdgeRuleMatchPath(pattern) == nil {
			t.Errorf("accepted invalid glob %q", pattern)
		}
	}
	for _, pattern := range []string{"/", "/admin/**", "/users/?*", "/api/[0-9]/*"} {
		if problem := api.ValidateEdgeRuleMatchPath(pattern); problem != nil {
			t.Errorf("valid glob %q: %v", pattern, problem)
		}
	}
}

func TestEdgeRuleHostStoreWildcardSemantics(t *testing.T) {
	for _, tc := range []struct {
		pattern, host string
		want          bool
	}{
		{"api_.example.com", "api1.example.com", true},
		{"api%.example.com", "api-one.example.com", true},
		{"api?.example.com", "apié.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "a.b.example.com", true},
		{`api\_.example.com`, "api_.example.com", true},
		{`api\_.example.com`, "api1.example.com", false},
		{"api[12].example.com", "api1.example.com", false},
		{"API.EXAMPLE.COM", "api.example.com", true},
	} {
		if got := api.EdgeRuleHostMatches(tc.pattern, tc.host); got != tc.want {
			t.Errorf("%q against %q = %v", tc.pattern, tc.host, got)
		}
	}
}
