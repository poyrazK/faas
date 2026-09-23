package api

import "testing"

func TestServiceBindingPolicyEffective(t *testing.T) {
	tests := []struct {
		name string
		in   ServiceBindingPolicy
		want ServiceBindingPolicy
	}{
		{name: "legacy empty defaults to account", want: ServiceBindingPolicyAccount},
		{name: "explicit account", in: ServiceBindingPolicyAccount, want: ServiceBindingPolicyAccount},
		{name: "declared", in: ServiceBindingPolicyDeclared, want: ServiceBindingPolicyDeclared},
		{name: "unknown fails closed", in: ServiceBindingPolicy("future-policy"), want: ServiceBindingPolicyDeclared},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.in.Effective(); got != test.want {
				t.Fatalf("Effective() = %q, want %q", got, test.want)
			}
		})
	}
}
