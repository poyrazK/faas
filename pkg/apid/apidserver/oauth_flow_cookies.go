package apidserver

import "net/http"

// An OAuth sign-in keeps its state, nonce and PKCE verifier in short-lived
// cookies until the provider redirects back. Apps serve from sibling hosts of
// the platform's registrable domain, so an app could set an unprefixed cookie
// for the whole domain: planting the state and verifier of a flow the
// attacker started would let the victim's browser finish that flow and sign
// in to the attacker's account (login CSRF; security audit 2026-10-27). Over
// HTTPS the cookies are therefore host-only `__Host-` cookies, which no other
// host can set, and only that name is read. Browsers refuse the prefix
// without Secure, so plain-HTTP development keeps the bare name and the
// callback path.

const hostCookiePrefix = "__Host-"

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS
}

// oauthFlowCookieName is the name a flow cookie has on this request.
func oauthFlowCookieName(r *http.Request, base string) string {
	if requestIsHTTPS(r) {
		return hostCookiePrefix + base
	}
	return base
}

// setOAuthFlowCookie sets one flow cookie for five minutes. callbackPath
// scopes it in development; a `__Host-` cookie must use Path=/.
func setOAuthFlowCookie(w http.ResponseWriter, r *http.Request, base, value, callbackPath string) {
	secure := requestIsHTTPS(r)
	path := callbackPath
	if secure {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthFlowCookieName(r, base),
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300, // 5 minutes
	})
}

// oauthFlowCookie reads a flow cookie by the name this request uses.
func oauthFlowCookie(r *http.Request, base string) (*http.Cookie, error) {
	return r.Cookie(oauthFlowCookieName(r, base))
}
