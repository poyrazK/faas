package api

import (
	"fmt"
	"html"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// AcceptsHTML reports whether the request explicitly accepts text/html with a
// non-zero quality value. A wildcard does not opt into the browser page: API
// clients commonly send */* and must continue receiving problem+json.
func AcceptsHTML(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, value := range strings.Split(r.Header.Get("Accept"), ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil || !strings.EqualFold(mediaType, "text/html") {
			continue
		}
		if rawQ, ok := params["q"]; ok {
			q, err := strconv.ParseFloat(rawQ, 64)
			if err != nil || q <= 0 {
				continue
			}
		}
		return true
	}
	return false
}

// writeProblemHTML deliberately omits Problem.Detail because gateway details
// may contain hostnames, app slugs, or internal diagnostics. Title and Hint
// are explicitly customer-facing fields, so the page can explain the failure
// and next action without exposing the underlying detail. The stable code is
// visible as a support handle and remains available to browser tooling.
func writeProblemHTML(w http.ResponseWriter, p *Problem) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Add("Vary", "Accept")
	w.Header().Set("Cache-Control", "no-store")
	for k, vs := range p.extraHeaders {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(p.Status)

	status := http.StatusText(p.Status)
	if status == "" {
		status = "Request error"
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = status
	}
	hint := strings.TrimSpace(p.Hint)
	if hint == "" {
		hint = "Please try again, or return to the app later."
	}
	docsURL := problemDocsURL(p.DocsURL)
	code := html.EscapeString(p.Code)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="faas-error-code" content="%s">
  <title>Gregale — %s</title>
  <style>
    :root { color-scheme: dark; font-family: system-ui, sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #101318; color: #eef1f5; }
    main { width: min(32rem, calc(100%% - 3rem)); padding: 2.5rem; border: 1px solid #303743; border-radius: 1rem; background: #171b22; box-shadow: 0 1rem 3rem #0005; }
    .mark { color: #8ab4ff; font-weight: 700; letter-spacing: .04em; }
    h1 { margin: 1.25rem 0 .75rem; font-size: 1.7rem; }
    p { color: #b8c0cc; line-height: 1.55; }
    code { color: #d8dee9; }
    a { color: #9cc1ff; }
  </style>
</head>
<body>
  <main data-faas-error-code="%s">
    <div class="mark">Gregale</div>
    <h1>%s</h1>
    <p>%s</p>
    <p>Error code: <code>%s</code></p>
    <p><a href="%s">View recovery guidance</a></p>
  </main>
</body>
</html>
`, code, html.EscapeString(title), code, html.EscapeString(title), html.EscapeString(hint), code, html.EscapeString(docsURL))
}

// problemDocsURL accepts Gregale documentation and status links only. Problem
// bodies can be reconstructed from upstream responses, so an arbitrary
// docs_url must not turn the error page into an open-redirect or phishing link.
func problemDocsURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return docsBase
	}
	u, err := url.Parse(raw)
	if err != nil {
		return docsBase
	}
	if u.IsAbs() {
		if !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Host, "gregale.dev") || u.User != nil {
			return docsBase
		}
		return u.String()
	}
	if u.Scheme != "" || u.Host != "" || strings.ContainsAny(raw, "\\\r\n") ||
		(u.Path != "/docs" && !strings.HasPrefix(u.Path, "/docs/")) {
		return docsBase
	}
	return u.String()
}
