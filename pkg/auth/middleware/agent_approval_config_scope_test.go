package middleware

import (
	"net/http/httptest"
	"testing"
)

// A production config write asks for approval; a stage-scope write never
// reaches production traffic (apps/projects review 2026-11-10, P10).
func TestProductionConfigScope(t *testing.T) {
	for target, want := range map[string]bool{
		"/v1/apps/a/env/MODE":                                   true,
		"/v1/apps/a/env/MODE?scope=default":                     true,
		"/v1/apps/a/env/MODE?scope=production":                  true,
		"/v1/apps/a/env/MODE?scope=staging":                     false,
		"/v1/apps/a/secret-references/K?environment=staging":    false,
		"/v1/apps/a/secret-references/K?environment=production": true,
	} {
		if got := productionConfigScope(httptest.NewRequest("PUT", target, nil)); got != want {
			t.Errorf("%s: production = %v, want %v", target, got, want)
		}
	}
}
