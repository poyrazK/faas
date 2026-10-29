package apidserver

import (
	"encoding/base64"
	"net/http"
	"os"
)

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

// oauthNextCookie carries where a sign-in continues, so a sign-in started from
// /login?next=… lands there instead of on the dashboard (ADR-20261028205920).
const oauthNextCookie = "faas_oauth_next"

// setOAuthNext keeps the request's validated next path for the callback, or
// clears one a previous sign-in left.
func setOAuthNext(w http.ResponseWriter, r *http.Request, callbackPath string) {
	raw := r.URL.Query().Get("next")
	if raw == "" {
		clearOAuthNext(w, r, callbackPath)
		return
	}
	setOAuthFlowCookie(w, r, oauthNextCookie, base64.RawURLEncoding.EncodeToString([]byte(dashboardMFANext(raw))), callbackPath)
}

func clearOAuthNext(w http.ResponseWriter, r *http.Request, callbackPath string) {
	secure := requestIsHTTPS(r)
	path := callbackPath
	if secure {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{Name: oauthFlowCookieName(r, oauthNextCookie), Value: "", Path: path, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// finishOAuthSignIn redirects a signed-in browser to the path its sign-in
// started from, else to the website as before.
func finishOAuthSignIn(w http.ResponseWriter, r *http.Request, callbackPath string) {
	target := oauthNext(r)
	if target == "" {
		target = os.Getenv("WEBSITE_URL")
	}
	if target == "" {
		target = "/"
	}
	clearOAuthNext(w, r, callbackPath)
	http.Redirect(w, r, target, http.StatusFound)
}

// oauthNext is the validated path the callback continues to, or "" without one.
func oauthNext(r *http.Request) string {
	cookie, err := oauthFlowCookie(r, oauthNextCookie)
	if err != nil || cookie.Value == "" {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(raw) == 0 {
		return ""
	}
	return dashboardMFANext(string(raw))
}
