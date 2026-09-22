package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Issue #3362: deploy printed "verified, 200" (the verifier authenticates)
// while an anonymous curl got 401, with no hint that the URL requires auth.
func TestRenderDeploymentAccess(t *testing.T) {
	cases := []struct {
		name string
		app  api.AppResponse
		want []string
	}{
		{name: "open app prints nothing", app: api.AppResponse{}},
		{
			name: "bearer",
			app:  api.AppResponse{RequireAuthn: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeBearer}},
			want: []string{"Authorization: Bearer", "gregale app my-api --no-require-authn"},
		},
		{
			name: "basic",
			app:  api.AppResponse{RequireAuthn: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeBasic, HasBasicCreds: true}},
			want: []string{"HTTP Basic", "gregale app my-api --no-require-authn"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			renderDeploymentAccess(&buf, tc.app, "my-api")
			got := buf.String()
			if len(tc.want) == 0 && got != "" {
				t.Fatalf("output = %q, want none", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output %q missing %q", got, w)
				}
			}
		})
	}
}
