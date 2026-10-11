package apidserver

import (
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
)

// renderAuthCapabilities (GET /v1/auth/capabilities) is the boot-resolved
// "which sign-in OAuth providers are configured" signal (issue #419 /
// ADR-046). Mounted behind sessionAuth (cmd/apid/server.go) so it is
// reachable only for authenticated dashboard sessions; the response
// shape is intentionally minimal because exposing more would let a
// leaked session probe the configured-provider set beyond what
// /v1/auth/{provider} already reveals via consent-redirect.
//
// handlers_google.go and handlers_github.go remain the source of
// truth for per-provider state — they read s.oauthConfig directly
// and use auth.SignInProvider.Enabled() to short-circuit disabled
// providers to 503 oauth_provider_unavailable. This endpoint exists
// only so the dashboard can read the same signal without scraping
// the consent routes.
//
// The response body shape is api.AuthCapabilities (DTO pinned by
// pkg/api/dto.go + api/openapi.yaml) so the cmd/apid/spec_compliance_test
// schema-parity gate stays green. handler-local types are not
// reintroduced here so a future schema change can't drift from the
// wire-shape without a DTO update.
func (s *server) renderAuthCapabilities(w http.ResponseWriter, r *http.Request) {
	resp := api.AuthCapabilities{
		Providers: api.AuthProviders{
			Google: api.OAuthProviderCapability{Enabled: s.oauthConfig.Enabled(auth.GoogleProviderName)},
			GitHub: api.OAuthProviderCapability{Enabled: s.oauthConfig.Enabled(auth.GitHubProviderName)},
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// json.Encode cannot fail here — encoder writes to a
	// successful http.ResponseWriter, and the body is a static
	// struct of bools. Drop the error explicitly so a future
	// change that touches the encoder doesn't accidentally drop
	// _ = again.
	_ = json.NewEncoder(w).Encode(resp)
}
