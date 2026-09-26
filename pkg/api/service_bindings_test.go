package api

import (
	"reflect"
	"strings"
	"testing"
)

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

func TestNormalizeAllowedServiceCallers(t *testing.T) {
	got, err := NormalizeAllowedServiceCallers([]string{" Worker ", "frontend", "FRONTEND"})
	if err != nil || !reflect.DeepEqual(got, []string{"frontend", "worker"}) {
		t.Fatalf("normalized callers = %v, %v", got, err)
	}
	empty, err := NormalizeAllowedServiceCallers(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("explicit empty callers = %v, %v", empty, err)
	}
	for _, name := range []string{"", "../other", "-caller", "caller-", "a.b"} {
		if _, err := NormalizeAllowedServiceCallers([]string{name}); err == nil {
			t.Errorf("accepted invalid caller %q", name)
		}
	}
	names := make([]string, AllowedServiceCallersMax+1)
	for i := range names {
		names[i] = "caller"
	}
	if _, err := NormalizeAllowedServiceCallers(names); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("over-limit error = %v", err)
	}
}
