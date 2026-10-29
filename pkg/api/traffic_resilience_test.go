package api

import "testing"

func TestEgressCircuitBreakerEnabled(t *testing.T) {
	for name, parse := range map[string]func(string) (bool, error){
		"egress circuit breaker": EgressCircuitBreakerEnabled,
		"scoped binding guard":   TrafficScopedBindingGuardEnabled,
	} {
		for _, tc := range []struct {
			value string
			want  bool
			bad   bool
		}{{"", false, false}, {"false", false, false}, {"0", false, false}, {"1", true, false}, {" true ", true, false}, {"maybe", false, true}} {
			t.Run(name+"/"+tc.value, func(t *testing.T) {
				got, err := parse(tc.value)
				if got != tc.want || (err != nil) != tc.bad {
					t.Fatalf("got=%v error=%v", got, err)
				}
			})
		}
	}
}
