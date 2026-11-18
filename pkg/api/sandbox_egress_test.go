// adr: 20261117091026 §1 — a domain rule is a lowercase public name; it
// matches its name and every name below it; deny wins, and an empty allow
// allows what the platform does.
package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSandboxEgressRules(t *testing.T) {
	for rule, ok := range map[string]bool{"example.com": true, "docs.example.com": true, "co.uk": true, "Example.com": false,
		"example.com.": false, "*.example.com": false, "localhost": false, "10.0.0.1": false, "": false, "svc.internal": false} {
		if got := api.ValidSandboxEgressRule(rule); got != ok {
			t.Errorf("ValidSandboxEgressRule(%q) = %v", rule, got)
		}
	}
	for _, tc := range []struct {
		allow, deny []string
		name        string
		want        bool
	}{
		{nil, nil, "anything.example", true},
		{[]string{"example.com"}, nil, "example.com", true},
		{[]string{"example.com"}, nil, "Docs.Example.com.", true},
		{[]string{"example.com"}, nil, "badexample.com", false},
		{[]string{"example.com"}, nil, "example.org", false},
		{nil, []string{"evil.com"}, "cdn.evil.com", false},
		{nil, []string{"evil.com"}, "notevil.com", true},
		{[]string{"example.com"}, []string{"admin.example.com"}, "admin.example.com", false},
		{[]string{"example.com"}, []string{"admin.example.com"}, "www.example.com", true},
	} {
		if got := api.SandboxEgressAllows(tc.allow, tc.deny, tc.name); got != tc.want {
			t.Errorf("allow %v deny %v %q = %v", tc.allow, tc.deny, tc.name, got)
		}
	}
	if !api.SandboxEgressRuleCovered("docs.example.com", []string{"example.com"}) || api.SandboxEgressRuleCovered("example.com", []string{"docs.example.com"}) {
		t.Fatal("coverage is the wrong way round")
	}
}
