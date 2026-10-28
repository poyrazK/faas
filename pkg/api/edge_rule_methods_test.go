package api

import (
	"slices"
	"testing"
)

// adr: 091 — amendment 2026-10-28 (review round 3): match_methods is a
// closed vocabulary, upper-cased on write. The gateway compares methods
// exactly, so "*", a typo or a trailing space matched nothing and a jwt or
// ip rule scoped by it silently never applied.
func TestNormalizeEdgeRuleMatchMethods(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      []string
		want    []string
		refused bool
	}{
		{name: "empty matches every method", in: nil, want: nil},
		{name: "lower case is upper-cased and duplicates dropped", in: []string{"get", "GET", "post"}, want: []string{"GET", "POST"}},
		{name: "every vocabulary method", in: []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE"},
			want: []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE"}},
		{name: "a wildcard", in: []string{"*"}, refused: true},
		{name: "a typo", in: []string{"GETS"}, refused: true},
		{name: "a trailing space", in: []string{"GET "}, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, prob := NormalizeEdgeRuleMatchMethods(tc.in)
			if tc.refused {
				if prob == nil {
					t.Fatalf("NormalizeEdgeRuleMatchMethods(%q) = %q, want a refusal", tc.in, got)
				}
				return
			}
			if prob != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("NormalizeEdgeRuleMatchMethods(%q) = %q, %v; want %q", tc.in, got, prob, tc.want)
			}
		})
	}
}

// adr: 091 — amendment 2026-10-28 (review round 3): X-Forwarded-For and
// Forwarded carry the client address the platform established, so a header
// op may not set or remove them; they stay out of the list the gateway
// applies to stored rules, so a rule saved before still compiles.
func TestEdgeRuleHeaderOpsCannotWriteForwardingHeaders(t *testing.T) {
	for _, name := range []string{"X-Forwarded-For", "x-forwarded-for", "Forwarded"} {
		op := EdgeRuleHeaderOp{Name: name, Value: "198.51.100.9", Action: "set"}
		if prob := op.Validate(); prob == nil {
			t.Errorf("header op on %q was accepted", name)
		}
		if EdgeRuleHeaderNameForbidden(name) {
			t.Errorf("%q joined the compile-time list; rules stored before the amendment would stop compiling", name)
		}
	}
	redirect := EdgeRuleRedirectAction{StatusCode: 302, To: "/new", Headers: map[string]string{"Forwarded": "for=198.51.100.9"}}
	if prob := redirect.Validate(); prob == nil {
		t.Error("redirect header Forwarded was accepted")
	}
}
