package api

import (
	"fmt"
	"html"
	"mime"
	"net/http"
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

// writeProblemHTML is deliberately generic and contains no problem detail:
// gateway errors may include hostnames, app slugs, or internal diagnostics.
// The stable problem code is retained in both machine-readable locations so
// browser tooling can branch without exposing customer data in the page.
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
    a { color: #9cc1ff; }
  </style>
</head>
<body>
  <main data-faas-error-code="%s">
    <div class="mark">Gregale</div>
    <h1>%s</h1>
    <p>Gregale could not complete this request. Please try again, or return to the app later.</p>
    <p><a href="%s">Open Gregale documentation</a></p>
  </main>
</body>
</html>
`, code, html.EscapeString(status), code, html.EscapeString(status), docsBase)
}
