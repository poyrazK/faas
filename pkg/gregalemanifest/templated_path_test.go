package gregalemanifest

import (
	"strings"
	"testing"
)

func TestTemplatedPathWarnings(t *testing.T) {
	m := &Manifest{AsyncRoutes: []AsyncRoute{
		{App: "api", Name: "ok", MatchHost: "api.example.com", MatchPath: "/reports/*"},
		{App: "api", Name: "dead", MatchHost: "api.example.com", MatchPath: "/reports/{id}/export"},
	}}
	warnings := m.TemplatedPathWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "async_routes[1]") || !strings.Contains(warnings[0], `"/reports/?*/export"`) {
		t.Fatalf("warnings = %q", warnings)
	}
	// Warnings never block a deploy: the manifest still validates.
	if err := m.AsyncRoutes[1].Validate(1); err != nil {
		t.Fatalf("templated route became an error: %v", err)
	}
	if got := (*Manifest)(nil).TemplatedPathWarnings(); got != nil {
		t.Fatalf("nil manifest warnings = %q", got)
	}
}
