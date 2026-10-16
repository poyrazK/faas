package api_test

import (
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

func TestNormalizeEnvironmentCacheRules(t *testing.T) {
	valid := func() []api.EnvironmentCacheRule {
		return []api.EnvironmentCacheRule{{Path: "/catalog/*", Priority: 1, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120, Methods: []string{"GET"}}}}
	}
	for _, tc := range []struct {
		name   string
		change func([]api.EnvironmentCacheRule) []api.EnvironmentCacheRule
	}{
		{"omitted", func(_ []api.EnvironmentCacheRule) []api.EnvironmentCacheRule { return nil }},
		{"unsafe_method", func(r []api.EnvironmentCacheRule) []api.EnvironmentCacheRule {
			r[0].Cache.Methods = []string{"POST"}
			return r
		}},
		{"credential_vary", func(r []api.EnvironmentCacheRule) []api.EnvironmentCacheRule {
			r[0].Cache.VaryOn = []string{"Authorization"}
			return r
		}},
		{"template", func(r []api.EnvironmentCacheRule) []api.EnvironmentCacheRule { r[0].Path = "/catalog/{id}"; return r }},
		{"glob", func(r []api.EnvironmentCacheRule) []api.EnvironmentCacheRule { r[0].Path = "/catalog/["; return r }},
		{"duplicate_priority", func(r []api.EnvironmentCacheRule) []api.EnvironmentCacheRule { return append(r, r[0]) }},
		{"bounded", func(r []api.EnvironmentCacheRule) []api.EnvironmentCacheRule {
			out := make([]api.EnvironmentCacheRule, api.EnvironmentCacheRuleLimit+1)
			for i := range out {
				out[i] = r[0]
				out[i].Priority = i
			}
			return out
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := api.NormalizeEnvironmentCacheRules(tc.change(valid())); err == nil {
				t.Fatal("invalid cache policy accepted")
			}
		})
	}
	if out, err := api.NormalizeEnvironmentCacheRules([]api.EnvironmentCacheRule{}); err != nil || out == nil || len(out) != 0 {
		t.Fatal("explicit empty override rejected")
	}
	input := valid()
	out, err := api.NormalizeEnvironmentCacheRules(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Cache.Methods[0] = "POST"
	if out[0].Cache.Methods[0] != "GET" {
		t.Fatal("policy aliases caller input")
	}
}
