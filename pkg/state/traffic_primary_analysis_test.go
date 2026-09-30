// adr: 375
package state

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTrafficPrimaryAnalysisScopeTransitions(t *testing.T) {
	host := "new.apps.example.test"
	over := int64(api.TrafficPolicyMaxHostBytes + 100)
	for _, test := range []struct {
		name   string
		group  trafficHostGroup
		assets []trafficHostAsset
		scope  string
	}{
		{"canonical", trafficHostGroup{Pattern: "*", Rows: 1, Canonical: over, Compiled: 10}, nil, "host_rule_projection"},
		{"sibling-compiler-retained", trafficHostGroup{App: "sibling", Pattern: "*", Rows: 1, Canonical: 10, Compiled: over}, nil, "host_compiled_projection_estimate"},
		{"distinct-preset-retained", trafficHostGroup{App: "sibling", Pattern: "*", Rows: 1, Canonical: 10, Compiled: 10, Preset: "large"}, []trafficHostAsset{{ID: "large", Compiled: over}}, "host_compiled_projection_estimate"},
		{"count", trafficHostGroup{Pattern: "*", Rows: api.TrafficPolicyMaxHostRules + 1}, nil, "host_rule_count"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := trafficHostAnalysis{Groups: []trafficHostGroup{test.group}, Assets: test.assets}
			after := before
			after.PrimaryHosts = []string{host}
			var aggregate *TrafficPolicyAggregateError
			if err := checkTrafficHostAnalysis(t.Context(), before, after); !errors.As(err, &aggregate) || aggregate.Scope != test.scope || aggregate.Host != host {
				t.Fatalf("new primary scope accepted overload: %v", err)
			}
			if err := checkTrafficHostAnalysis(t.Context(), after, after); err != nil {
				t.Fatalf("unchanged legacy projection refused: %v", err)
			}
			if err := checkTrafficHostAnalysis(t.Context(), after, before); err != nil {
				t.Fatalf("removed reserved primary URL fell back to discovery: %v", err)
			}
		})
	}
}

func TestTrafficPrimaryMarkerIsLiteral(t *testing.T) {
	before := trafficHostAnalysis{Groups: []trafficHostGroup{{Pattern: "new-a.apps.test", Rows: 1, Canonical: api.TrafficPolicyMaxHostBytes + 1}}}
	after := before
	after.PrimaryHosts = []string{"new_%.apps.test"}
	if err := checkTrafficHostAnalysis(t.Context(), before, after); err != nil {
		t.Fatalf("legacy slug marker acquired selector semantics: %v", err)
	}
}

func TestTrafficAliasCompilerRetainsAccountRulesAndPresets(t *testing.T) {
	host := "tag-candidate-0123456789ab4cde8123456789abcdef.apps.test"
	for _, preset := range []bool{false, true} {
		before := trafficHostAnalysis{Groups: []trafficHostGroup{{App: "sibling", Pattern: host, Rows: 1, Canonical: 10, Compiled: api.TrafficPolicyMaxHostBytes + 1}}}
		if preset {
			before.Groups[0].Compiled = 10
			before.Groups[0].Preset = "shared"
			before.Assets = []trafficHostAsset{{ID: "shared", Compiled: api.TrafficPolicyMaxHostBytes + 1}}
		}
		after := before
		after.AliasHosts = []string{host}
		var aggregate *TrafficPolicyAggregateError
		if err := checkTrafficHostAnalysis(t.Context(), before, after); !errors.As(err, &aggregate) || aggregate.Scope != "host_compiled_projection_estimate" || aggregate.Host != host {
			t.Fatalf("alias acquired an environment app filter: %v", err)
		}
	}
}

func TestMemTrafficAppsDomainConfiguration(t *testing.T) {
	for _, test := range []struct{ name, domain, want string }{
		{"custom", " .APPS.EXAMPLE.TEST ", ".apps.example.test"},
		{"disabled", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := NewMemStore(WithTrafficAppsDomain(test.domain))
			if m.trafficAppsSuffix != test.want {
				t.Fatalf("suffix=%q want=%q", m.trafficAppsSuffix, test.want)
			}
		})
	}
}
