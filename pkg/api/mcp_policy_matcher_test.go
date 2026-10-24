package api

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// uncachedMCPResourceAllowed is the per-call compile the matcher cache
// replaced; the cached path must decide every case the same way.
func uncachedMCPResourceAllowed(policy map[string][]string, uri string, granted []string) bool {
	if policy == nil {
		return true
	}
	matched := false
	for template, required := range policy {
		pattern := regexp.QuoteMeta(template)
		for _, variable := range mcpSimpleVariable.FindAllString(template, -1) {
			pattern = strings.ReplaceAll(pattern, regexp.QuoteMeta(variable), `[^/?#]+`)
		}
		if ok, _ := regexp.MatchString("^"+pattern+"$", uri); ok {
			matched = true
			if !MCPHasScopes(required, granted) {
				return false
			}
		}
	}
	return matched
}

func TestMCPResourceAllowedCachedMatchesUncached(t *testing.T) {
	policies := []map[string][]string{
		nil,
		{"file:///{path}": {"read"}},
		{"db://{table}/{id}": {"read"}, "db://users/{id}": {"admin"}},
		{"weird+[chars].(x)/{a}": {"read"}, "literal://exact": nil},
		{"{a}{b}": {"read"}},
	}
	uris := []string{"file:///etc", "file:///a/b", "db://users/1", "db://orders/9", "db://users/1/x",
		"weird+[chars].(x)/v", "literal://exact", "literal://exactly", "", "xy", "a?b"}
	grants := [][]string{nil, {"read"}, {"read", "admin"}}
	for round := 0; round < 2; round++ { // the second round is served from the cache
		for i, policy := range policies {
			for _, uri := range uris {
				for _, granted := range grants {
					if got, want := MCPResourceAllowed(policy, uri, granted), uncachedMCPResourceAllowed(policy, uri, granted); got != want {
						t.Fatalf("round %d policy %d uri %q granted %v: got %v, want %v", round, i, uri, granted, got, want)
					}
				}
			}
		}
	}
}

func TestMCPTemplateMatcherCacheIsBounded(t *testing.T) {
	for i := 0; i < mcpTemplateMatcherCacheMax+10; i++ {
		mcpTemplateMatcher(fmt.Sprintf("res://%d/{id}", i))
	}
	mcpTemplateMatchers.Lock()
	n := len(mcpTemplateMatchers.byTemplate)
	mcpTemplateMatchers.Unlock()
	if n > mcpTemplateMatcherCacheMax {
		t.Fatalf("cache holds %d templates; cap is %d", n, mcpTemplateMatcherCacheMax)
	}
	if !MCPResourceAllowed(map[string][]string{"res://1/{id}": nil}, "res://1/x", nil) {
		t.Fatal("matcher wrong after the cache was reset")
	}
}

func BenchmarkMCPResourceAllowed(b *testing.B) {
	policy := map[string][]string{}
	for i := 0; i < 32; i++ {
		policy[fmt.Sprintf("db://t%d/{id}", i)] = []string{"read"}
	}
	for i := 0; i < b.N; i++ {
		MCPResourceAllowed(policy, "db://t31/7", []string{"read"})
	}
}
