package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// disabledOAuthResponse is the shared 503
// `oauth_provider_unavailable` body the sign-in OAuth handlers emit
// when their provider's boot-resolved SignInConfig entry is
// Disabled (issue #419 / ADR-046). The four call sites are:
//
//   - GET /v1/auth/google         (handlers_google.go: renderGoogleAuthRedirect)
//   - GET /v1/auth/google/callback (handlers_google.go: handleGoogleOAuthCallback)
//   - GET /v1/auth/github         (handlers_github.go: renderGitHubAuthRedirect)
//   - GET /v1/auth/github/callback (handlers_github.go: handleGitHubOAuthCallback)
//
// Centralising the response keeps the wire shape, the
// observability hook (apid_oauth_disabled_total{provider}), and the
// operator-actionable log line in lock-step across the four sites —
// a future change to the disabled shape lands in one place.
//
// Parameters:
//   - providerName: lowercase "google" or "github"; used for the
//     metric label and the log key. Callers MUST pass one of the
//     pkg/auth constants (GoogleProviderName / GitHubProviderName)
//     so the metric label stays inside the closed set.
//   - missingEnv: a free-form hint describing which env vars are
//     unset. e.g. "GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET unset".
//     Surfaced in the WARN log so an operator scanning slog output
//     can act without opening the docs.
//   - detail: human-readable, customer-facing detail that names the
//     provider in the body.
func (s *server) disabledOAuthResponse(w http.ResponseWriter, providerName, missingEnv, detail string) {
	s.log.Warn(providerName+" OAuth disabled on this host",
		"missing_env", missingEnv,
		"provider", providerName)
	s.ops.ObserveOAuthDisabled(providerName)
	api.WriteProblem(w, api.NewProblem(
		http.StatusServiceUnavailable,
		api.CodeOAuthProviderUnavailable,
		"OAuth Provider Unavailable",
		detail,
	).WithHint("Use another sign-in method, or contact support if you need this provider.").WithDocs("https://gregale.dev/auth"))
}
