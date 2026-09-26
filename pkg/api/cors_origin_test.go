package api

import "testing"

func TestMatchEdgeRuleCORSOrigin(t *testing.T) {
	tests := []struct {
		name   string
		allow  []string
		origin string
		want   string
	}{
		{name: "empty origin", allow: []string{"https://app.example.com"}},
		{name: "literal is case insensitive", allow: []string{"https://app.example.com"}, origin: "HTTPS://App.Example.COM", want: "https://app.example.com"},
		{name: "full wildcard", allow: []string{"*"}, origin: "https://app.example.com", want: "*"},
		{name: "single label wildcard", allow: []string{"https://*.example.com"}, origin: "https://app.example.com", want: "https://*.example.com"},
		{name: "does not match nested subdomain", allow: []string{"https://*.example.com"}, origin: "https://app.sub.example.com"},
		{name: "port wildcard", allow: []string{"https://localhost:*"}, origin: "https://localhost:8443", want: "https://localhost:*"},
		{name: "port wildcard requires a port", allow: []string{"https://localhost:*"}, origin: "https://localhost"},
		{name: "scheme must match", allow: []string{"https://app.example.com"}, origin: "http://app.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchEdgeRuleCORSOrigin(tc.allow, tc.origin); got != tc.want {
				t.Fatalf("MatchEdgeRuleCORSOrigin(%v, %q) = %q, want %q", tc.allow, tc.origin, got, tc.want)
			}
		})
	}
}
