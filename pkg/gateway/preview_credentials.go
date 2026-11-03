package gateway

import "net/http"

// withholdPreviewGateCredential removes the Authorization header a platform
// gate verified before a preview's request goes upstream
// (ADR-20261102100001). A preview runs unreviewed code behind its production
// app's gates: the credential such a gate reads is the platform's — the
// production app's password, or a Gregale API key of the owning account —
// and handing it on let the pull request's code read it. The request stays
// marked as credentialed, so shared caches still bypass it.
//
// Production apps keep receiving the header, as before; so does a preview
// whose Authorization no platform gate reads (an app's own scheme).
func withholdPreviewGateCredential(r *http.Request, app App) {
	if !app.IsPreview || !platformGateReadsAuthorization(app) || len(r.Header.Values("Authorization")) == 0 {
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
