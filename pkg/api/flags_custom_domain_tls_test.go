package api

import "testing"

func TestCustomDomainTLSOnDemand(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"on_demand", true},
		{" ON_DEMAND ", true},
		{"dns01", false},
		{"1", false},
	} {
		t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", tc.value)
		if got := CustomDomainTLSOnDemand(); got != tc.want {
			t.Errorf("CustomDomainTLSOnDemand(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
