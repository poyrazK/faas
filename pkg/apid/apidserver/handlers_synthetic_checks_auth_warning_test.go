package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSyntheticCheckAuthWarning pins that a check on an app whose platform
// auth refuses credential-less probes says so, unless it expects the refusal.
func TestSyntheticCheckAuthWarning(t *testing.T) {
	for _, tc := range []struct {
		name     string
		app      state.App
		expected int
		want     string
	}{
		{name: "open app", app: state.App{PublicAuthMode: api.AppPublicAuthModeOpen}},
		{name: "bearer", app: state.App{PublicAuthMode: api.AppPublicAuthModeBearer}, want: "bearer auth"},
		{name: "require authn", app: state.App{RequireAuthn: true}, want: "bearer auth"},
		{name: "basic", app: state.App{PublicAuthMode: api.AppPublicAuthModeBasic}, want: "basic auth"},
		{name: "consumer keys required", app: state.App{ConsumerAuthMode: state.ConsumerAuthModeRequired}, want: "consumer keys"},
		{name: "bearer expecting 401", app: state.App{PublicAuthMode: api.AppPublicAuthModeBearer}, expected: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := apidserver.SyntheticCheckAuthWarning(tc.app, tc.expected)
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("warning = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}
