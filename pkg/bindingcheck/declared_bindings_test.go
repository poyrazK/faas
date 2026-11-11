package bindingcheck

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEvaluateDeclaredBindings(t *testing.T) {
	cases := []struct {
		name     string
		declared []DeclaredBinding
		empty    bool
		blocker  string
		passed   bool
	}{
		{name: "declared and bound", declared: []DeclaredBinding{{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL"}}, passed: true},
		{name: "declared for this scope but missing", declared: []DeclaredBinding{{Type: api.BindingTypePostgres, Name: "replica", Binding: "REPLICA_URL", Scope: "production"}}, blocker: "declared_binding_missing"},
		{name: "declared for another scope", declared: []DeclaredBinding{{Type: api.BindingTypePostgres, Name: "replica", Binding: "REPLICA_URL", Scope: "staging"}}, passed: true},
		{name: "declared with zero bindings", declared: []DeclaredBinding{{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL"}}, empty: true, blocker: "no_bindings"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inventory, policy, now := fixture()
			if tc.empty {
				inventory.Bindings = nil
			}
			policy.DeclaredBindings = tc.declared
			report, err := Evaluate(inventory, policy, now)
			if err != nil {
				t.Fatal(err)
			}
			if tc.blocker != "" && !slices.Contains(codes(report.Blockers), tc.blocker) {
				t.Fatalf("blockers = %v, want %s", codes(report.Blockers), tc.blocker)
			}
			if tc.blocker == "" && report.Passed != tc.passed {
				t.Fatalf("passed = %v, blockers = %v", report.Passed, codes(report.Blockers))
			}
		})
	}
}
