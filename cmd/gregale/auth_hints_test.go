package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #6 (H5-58): `gregale app <slug> --public-auth open`
// printed "Updated" while every request still got 401 from require_authn,
// which new apps have on by default.
func TestRequireAuthnStillOnHint(t *testing.T) {
	open := &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}
	basic := &api.PublicAuthBlock{Mode: api.AppPublicAuthModeBasic}
	cases := []struct {
		name     string
		req      api.UpdateAppRequest
		updated  api.AppResponse
		wantHint bool
	}{
		{"open with require_authn on", api.UpdateAppRequest{PublicAuth: open}, api.AppResponse{RequireAuthn: true}, true},
		{"open with require_authn off", api.UpdateAppRequest{PublicAuth: open}, api.AppResponse{RequireAuthn: false}, false},
		{"basic with require_authn on", api.UpdateAppRequest{PublicAuth: basic}, api.AppResponse{RequireAuthn: true}, false},
		{"no public-auth change", api.UpdateAppRequest{}, api.AppResponse{RequireAuthn: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requireAuthnStillOnHint(tc.req, tc.updated) != ""; got != tc.wantHint {
				t.Fatalf("hint shown = %v, want %v", got, tc.wantHint)
			}
		})
	}
}

// Disabling require_authn on an app whose public URL is already open must
// not tell the user to pass --public-auth open.
func TestOpenPublicAuthAfterTokenRemovalKeepsOtherModes(t *testing.T) {
	for mode, wantOpen := range map[string]bool{
		"":                                true,
		api.AppPublicAuthModeBearer:       true,
		api.AppPublicAuthModeOpen:         false,
		api.AppPublicAuthModeBasic:        false,
		api.AppPublicAuthModeIPAllowlist:  false,
		api.AppPublicAuthModeInternalOnly: false,
	} {
		got := openPublicAuthAfterTokenRemoval(api.AppResponse{PublicAuth: api.PublicAuthStatus{Mode: mode}})
		if (got != nil) != wantOpen {
			t.Fatalf("mode %q: opened=%v, want %v", mode, got != nil, wantOpen)
		}
	}
}
