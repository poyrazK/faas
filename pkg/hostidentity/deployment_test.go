// adr: 122, 531
package hostidentity

import (
	"strings"
	"testing"
)

func TestBuildDeploymentHostPreservesRuntimeGrammar(t *testing.T) {
	for _, fixture := range []struct {
		name, suffix, slug string
		revision           int
		valid              bool
	}{
		{"canonical", ".gregale.dev", "web-api", 42, true},
		{"legacy-hyphens", ".gregale.dev", "-web-", 1, true},
		{"legacy-long", ".gregale.dev", strings.Repeat("a", 80), 1, true},
		{"zero", ".gregale.dev", "web", 0, false},
		{"negative", ".gregale.dev", "web", -1, false},
		{"empty-slug", ".gregale.dev", "", 1, false},
		{"uppercase", ".gregale.dev", "Web", 1, false},
		{"underscore", ".gregale.dev", "web_api", 1, false},
		{"dot", ".gregale.dev", "web.api", 1, false},
		{"empty-suffix", "", "web", 1, false},
		{"undelimited-suffix", "gregale.dev", "web", 1, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			host := BuildDeploymentHost(fixture.suffix, fixture.revision, fixture.slug)
			if !fixture.valid {
				if host != "" {
					t.Fatalf("invalid writer input published %q", host)
				}
				return
			}
			revision, slug, ok := DeploymentScopeFromHost(fixture.suffix, host)
			if !ok || revision != fixture.revision || slug != fixture.slug {
				t.Fatalf("writer/runtime disagreement: %q -> %d %q %v", host, revision, slug, ok)
			}
		})
	}
	for _, host := range []string{"deploy-01-web.gregale.dev", "deploy-0-web.gregale.dev", "deploy-+1-web.gregale.dev", "deploy-999999999999999999999999-web.gregale.dev"} {
		if _, _, ok := DeploymentScopeFromHost(DeployWildcardSuffix, host); ok {
			t.Fatalf("noncanonical revision accepted: %q", host)
		}
	}
}
