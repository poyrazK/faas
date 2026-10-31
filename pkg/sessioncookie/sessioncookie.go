// Package sessioncookie names the dashboard and API session cookie
// (ADR-20261026112500).
//
// The cookie is __Host-faas_sid. Browsers accept a __Host- cookie only when
// it is Secure, has Path=/ and no Domain attribute, so no other host of the
// registrable domain (a tenant app on <slug>.gregale.dev) can set or shadow
// it. The unprefixed faas_sid is still read while sessions issued before the
// change expire, and is written only on a plain-HTTP install, where a
// browser refuses a __Host- cookie.
package sessioncookie

import "net/http"

const (
	// Name is the session cookie.
	Name = "__Host-faas_sid"
	// LegacyName is the cookie before ADR-20261026112500.
	LegacyName = "faas_sid"
)

// IsName reports whether name is either session cookie name.
func IsName(name string) bool {
	return name == Name || name == LegacyName
}

// Get returns the request's session cookie, preferring Name, with the error
// semantics of (*http.Request).Cookie. A legacy cookie is used only when no
// __Host- cookie is present, so a tenant-set faas_sid cannot replace a
// session the browser holds under Name.
func Get(r *http.Request) (*http.Cookie, error) {
	if c, err := r.Cookie(Name); err == nil && c.Value != "" {
		return c, nil
	}
	return r.Cookie(LegacyName)
}

// Set writes the session cookie c; c.Name is ignored. A Secure cookie is
// written as Name, with Path=/ and no Domain as the prefix requires, and the
// legacy cookie is expired so the browser stops sending that name. An
// insecure cookie keeps LegacyName. An expiring c (MaxAge < 0) clears the
// session.
func Set(w http.ResponseWriter, c http.Cookie) {
	if !c.Secure {
		c.Name = LegacyName
		http.SetCookie(w, &c)
		return
	}
	c.Name, c.Path, c.Domain = Name, "/", ""
	http.SetCookie(w, &c)
	legacy := c
	legacy.Name, legacy.Value, legacy.MaxAge = LegacyName, "", -1
	http.SetCookie(w, &legacy)
}

// Clear expires both cookies. legacyDomain is the Domain attribute a legacy
// cookie was issued with ("" for host-only).
func Clear(w http.ResponseWriter, legacyDomain string) {
	http.SetCookie(w, &http.Cookie{Name: Name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: LegacyName, Value: "", Path: "/", MaxAge: -1,
		Domain: legacyDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// AddToRequest attaches a session value under both names, for clients (the
// CLIs) that must work against a server before and after the change.
func AddToRequest(req *http.Request, value string) {
	req.AddCookie(&http.Cookie{Name: Name, Value: value})
	req.AddCookie(&http.Cookie{Name: LegacyName, Value: value})
}

// FromResponse returns the session cookie a response sets, preferring Name
// (a new server also expires LegacyName alongside it). An empty value means
// the server cleared the session.
func FromResponse(resp *http.Response) (*http.Cookie, bool) {
	var legacy *http.Cookie
	for _, c := range resp.Cookies() {
		switch c.Name {
		case Name:
			return c, true
		case LegacyName:
			legacy = c
		}
	}
	return legacy, legacy != nil
}
