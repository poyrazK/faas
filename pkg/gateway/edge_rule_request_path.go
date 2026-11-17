package gateway

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// normalizeRequestPath rewrites r's path to api.NormalizeEdgeRequestPath:
// dot segments resolved and duplicate slashes collapsed, as most frameworks
// route it. Edge rules then select the rule for the path the app serves, and
// the app receives that path (ADR-20261108100000). A path already in that
// form, the common case, is left untouched, escapes included. A rewritten
// path drops RawPath, so an encoded slash inside such a request is forwarded
// as the "/" rules matched it as. r.RequestURI keeps the request line as
// received for the WAF (originalRequestURI).
func normalizeRequestPath(r *http.Request) {
	if r == nil || r.URL == nil {
		return
	}
	normalized := api.NormalizeEdgeRequestPath(r.URL.Path)
	if normalized == r.URL.Path {
		return
	}
	r.URL.Path, r.URL.RawPath = normalized, ""
}

// originalRequestURI is the request target as the client sent it, before
// normalizeRequestPath, so WAF signatures still see traversal sequences such
// as "../". Requests built in-process (no request line) fall back to the URL.
// An absolute-form target is reduced to its origin form, as the URL gives it.
func originalRequestURI(r *http.Request) string {
	if strings.HasPrefix(r.RequestURI, "/") {
		return r.RequestURI
	}
	if r.RequestURI != "" && r.RequestURI != "*" {
		if u, err := url.ParseRequestURI(r.RequestURI); err == nil {
			return u.RequestURI()
		}
	}
	return r.URL.RequestURI()
}
