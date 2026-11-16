package mcpmanagement

import (
	"crypto/sha256"
	"encoding/base64"
	"html"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// The document has no scripts, fonts or external assets. Only this exact
// stylesheet is allowed by CSP; form submissions remain same-origin.
const consentStyles = `
:root{color-scheme:light dark;--bg:#f5f6f8;--card:#fff;--fg:#18202b;--muted:#566170;--line:#dce1e7;--accent:#224da8;font:15px/1.6 ui-sans-serif,system-ui,sans-serif}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg)}main{width:min(42rem,calc(100% - 2rem));margin:3rem auto;padding:2rem;background:var(--card);border:1px solid var(--line);border-radius:14px}
.brand{font-weight:700;letter-spacing:.02em;color:var(--fg);margin:0 0 1.5rem}h1{font-size:1.7rem;line-height:1.25;letter-spacing:-.025em;margin:0 0 1rem}p{margin:0 0 1rem;color:var(--muted)}strong,legend{color:var(--fg)}
.steps{font-size:.85rem;margin:0 0 1.5rem;color:var(--muted)}fieldset,details{border:1px solid var(--line);border-radius:8px;margin:1.25rem 0;padding:1rem}legend{padding:0 .35rem;font-weight:600}summary{cursor:pointer;font-weight:600}details[open]>summary{margin-bottom:.75rem}
label{line-height:1.6}select,input[type=password]{display:block;max-width:100%;width:100%;font:inherit;color:var(--fg);background:var(--card);border:1px solid var(--line);border-radius:6px;padding:.6rem;margin:.5rem 0 1rem}input[type=checkbox],input[type=radio]{accent-color:var(--accent);margin-right:.45rem}button,.button{display:inline-block;font:inherit;font-weight:600;border:1px solid var(--accent);border-radius:7px;padding:.6rem 1rem;margin:.5rem .5rem 0 0;background:var(--accent);color:#fff;cursor:pointer;text-decoration:none}button[value=deny]{background:transparent;border-color:var(--line);color:var(--fg)}:focus-visible{outline:3px solid var(--accent);outline-offset:3px}a{color:var(--accent)}.fine{font-size:.85rem}dl{margin:0;font-size:.85rem}dt{font-weight:600;margin-top:.75rem}dd{margin:0;color:var(--muted);overflow-wrap:anywhere}
@media(prefers-color-scheme:dark){:root{--bg:#101318;--card:#171c24;--fg:#eef1f5;--muted:#b8c0cc;--line:#343e4c;--accent:#8eb5ff}button,.button{color:#101318}}
@media(max-width:480px){main{margin:1rem auto;padding:1.25rem}h1{font-size:1.45rem}button,.button{width:100%;text-align:center}}
`

func setConsentHeaders(w http.ResponseWriter) {
	sum := sha256.Sum256([]byte(consentStyles))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", privateContentSecurityPolicy+"; style-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
}

func consentDocumentStart(title, head string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` + html.EscapeString(title) + `</title>` + head + `<style>` + consentStyles + `</style></head><body><main><p class="brand">Gregale</p>`
}

// Browser forms cannot follow cross-origin 303s under form-action 'self' in
// Chromium. Finish the POST with a document, then navigate independently of
// the form. No scripts or extra form destinations are needed. Callers supply
// only the checked native login URL or the registered OAuth callback.
func redirectOAuthBrowser(w http.ResponseWriter, req *http.Request, target, title string) {
	w.Header().Add("Vary", "Accept")
	if !api.AcceptsHTML(req) {
		http.Redirect(w, req, target, http.StatusSeeOther)
		return
	}
	setConsentHeaders(w)
	e := html.EscapeString
	_, _ = io.WriteString(w, consentDocumentStart(title, `<meta http-equiv="refresh" content="0;url=`+e(target)+`">`)+
		`<h1>`+e(title)+`</h1><p>Your browser will continue automatically. If it does not, use the button below.</p><a class="button" href="`+e(target)+`">`+e(title)+`</a></main></body></html>`)
}
