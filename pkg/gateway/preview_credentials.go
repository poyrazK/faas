package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// withholdPreviewGateCredential removes the Authorization header a platform
// gate verified before a preview's request goes upstream
// (ADR-20261102100001). A preview runs unreviewed code behind its production
// app's gates: the credential such a gate reads is the platform's — the
// production app's password, or a Gregale API key of the owning account —
// and handing it on let the pull request's code read it. The request stays
// marked as credentialed, so shared caches still bypass it.
//
// A verified consumer key is withheld the same way: on a preview it is
// usually a production consumer's key (ADR-20261105090226).
//
// A preview whose Authorization no platform gate reads (an app's own scheme)
// keeps it.
//
// A production app does not receive a Gregale API key either, once
// require_authn or bearer public auth has verified it
// (ADR-20261120060000): the key belongs to the caller's account, not the
// app, and app code that logs or forwards headers leaked it. A production
// app keeps its own basic-auth password, verified consumer keys, and any
// Authorization no platform gate reads.
func withholdPreviewGateCredential(r *http.Request, app App) {
	if len(r.Header.Values("Authorization")) == 0 {
		return
	}
	if !app.IsPreview {
		if !platformGateReadsAPIKey(app) || !api.ValidAPIKeyFormat(bearerTokenFromHeader(r.Header.Get("Authorization"))) {
			return
		}
	} else if !platformGateReadsAuthorization(app) && authenticatedFrom(r.Context()).ConsumerKeyID == "" {
		return
	}
	r.Header.Del("Authorization")
	authenticated := authenticatedFrom(r.Context())
	authenticated.CredentialWithheld = true
	*r = *r.WithContext(withAuthenticated(r.Context(), authenticated))
}

// platformGateReadsAuthorization reports whether a platform gate on app
// authenticates the request from its Authorization header: require_authn and
// the bearer and basic public-auth modes.
func platformGateReadsAuthorization(app App) bool {
	if app.RequireAuthn {
		return true
	}
	switch app.PublicAuth.Mode {
	case publicAuthModeBearer, publicAuthModeBasic:
		return true
	}
	return false
}

// platformGateReadsAPIKey reports whether a platform gate on app verifies a
// Gregale API key from the Authorization header: require_authn and bearer
// public auth.
func platformGateReadsAPIKey(app App) bool {
	return app.RequireAuthn || app.PublicAuth.Mode == publicAuthModeBearer
}
