package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"html"
	"io"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// challengeSolverJS finds a decimal counter for which
// SHA-256(token + ":" + counter) starts with difficulty zero bits. It is a
// self-contained SHA-256 over ASCII input, so the page needs no third-party
// code and runs where crypto.subtle is unavailable (plain HTTP in
// development). solve() is pure; the page part runs only in a browser, so a
// test can load this script in Node and call solve() directly.
const challengeSolverJS = `"use strict";
var K = new Uint32Array([
0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2]);
var W = new Uint32Array(64);
function sha256Word0(s) {
  var len = s.length, blocks = (len + 72) >> 6, m = new Uint8Array(blocks * 64), i;
  for (i = 0; i < len; i++) m[i] = s.charCodeAt(i) & 0xff;
  m[len] = 0x80;
  var bitLen = len * 8, end = blocks * 64;
  m[end - 4] = bitLen >>> 24; m[end - 3] = bitLen >>> 16; m[end - 2] = bitLen >>> 8; m[end - 1] = bitLen;
  var h0 = 0x6a09e667, h1 = 0xbb67ae85, h2 = 0x3c6ef372, h3 = 0xa54ff53a,
      h4 = 0x510e527f, h5 = 0x9b05688c, h6 = 0x1f83d9ab, h7 = 0x5be0cd19;
  for (var o = 0; o < end; o += 64) {
    for (i = 0; i < 16; i++) W[i] = (m[o + 4 * i] << 24) | (m[o + 4 * i + 1] << 16) | (m[o + 4 * i + 2] << 8) | m[o + 4 * i + 3];
    for (i = 16; i < 64; i++) {
      var x = W[i - 15], y = W[i - 2];
      var s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3);
      var s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10);
      W[i] = (W[i - 16] + s0 + W[i - 7] + s1) | 0;
    }
    var a = h0, b = h1, c = h2, d = h3, e = h4, f = h5, g = h6, h = h7;
    for (i = 0; i < 64; i++) {
      var S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
      var t1 = (h + S1 + ((e & f) ^ (~e & g)) + K[i] + W[i]) | 0;
      var S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
      var t2 = (S0 + ((a & b) ^ (a & c) ^ (b & c))) | 0;
      h = g; g = f; f = e; e = (d + t1) | 0; d = c; c = b; b = a; a = (t1 + t2) | 0;
    }
    h0 = (h0 + a) | 0; h1 = (h1 + b) | 0; h2 = (h2 + c) | 0; h3 = (h3 + d) | 0;
    h4 = (h4 + e) | 0; h5 = (h5 + f) | 0; h6 = (h6 + g) | 0; h7 = (h7 + h) | 0;
  }
  return h0 >>> 0;
}
function solve(token, difficulty, start, limit) {
  var prefix = token + ":";
  for (var n = start; n < start + limit; n++) {
    if (Math.clz32(sha256Word0(prefix + n)) >= difficulty) return n;
  }
  return -1;
}
if (typeof document !== "undefined") {
  var form = document.getElementById("gregale-challenge");
  var token = form.getAttribute("data-token"), difficulty = +form.getAttribute("data-difficulty");
  var next = 0;
  var step = function () {
    var n = solve(token, difficulty, next, 20000);
    if (n < 0) { next += 20000; setTimeout(step, 0); return; }
    form.elements.counter.value = String(n);
    form.submit();
  };
  setTimeout(step, 0);
}
`

// challengePageParts is the page around its values, each one a
// double-quoted attribute value or paragraph text, where escaping &, <, >,
// " and ' is complete. The solver script and the style are constants, so no
// request data can become script.
//
// Not html/template: executing a template reaches text/template's reflective
// field lookup (reflect.Value.MethodByName), which makes the linker keep
// every exported method of every type converted to an interface. That put
// all 4,169 *state.PgStore methods into gatewayd-internal, so any store
// change rebuilt the edge lane (docs/adr/20261020160744-daemon-blast-radius.md,
// finding 7; scripts/ci/daemon_deps.json keeps templates out of daemons).
var challengePageParts = [...]string{
	`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Checking your browser</title>
<style nonce="`, // nonce
	`">body{font:16px/1.5 system-ui,sans-serif;margin:0;display:grid;place-items:center;min-height:100vh;color:#1f2328;background:#fff}
main{max-width:28rem;padding:1.5rem;text-align:center}@media (prefers-color-scheme:dark){body{color:#e6edf3;background:#0d1117}}</style>
</head><body><main>
<h1>Checking your browser</h1>
<p>This site asks your browser to do a short calculation before loading. It takes a moment and happens once.</p>
<noscript><p>Turn on JavaScript to continue to this site.`, // reference
	`</p></noscript>
<form id="gregale-challenge" method="post" action="`, // action
	`" data-token="`,      // token
	`" data-difficulty="`, // difficulty
	`">
<input type="hidden" name="token" value="`, // token
	`"><input type="hidden" name="counter" value="">
<input type="hidden" name="return" value="`, // return
	`"></form>
</main><script nonce="`, // nonce
	`">` + challengeSolverJS + `</script></body></html>
`,
}

// renderChallengePage writes the page for d.
func renderChallengePage(w io.Writer, d challengePageData) error {
	p := challengePageParts
	values := [...]string{d.Nonce, d.Reference, d.Action, d.Token, strconv.Itoa(d.Difficulty), d.Token, d.Return, d.Nonce}
	n := len(p[len(p)-1])
	for i, v := range values {
		n += len(p[i]) + len(v)
	}
	b := make([]byte, 0, n+64)
	for i, v := range values {
		b = append(b, p[i]...)
		b = append(b, html.EscapeString(v)...)
	}
	b = append(b, p[len(p)-1]...)
	_, err := w.Write(b)
	return err
}

type challengePageData struct {
	Nonce, Action, Token, Return string
	Difficulty                   int
	// Reference is the sentence a visitor without JavaScript sees after
	// the request to turn it on: the request ID to give the site's owner,
	// who finds the matching challenged event (ADR-831 amendment 8).
	Reference string
}

// writeChallengePage answers a browser navigation with the challenge page.
// The page is never cached or indexed, may not be framed, and loads nothing
// but its own inline script and style.
func writeChallengePage(w http.ResponseWriter, r *http.Request, token string, difficulty int) {
	nonceBytes := make([]byte, 16)
	_, _ = rand.Read(nonceBytes)
	nonce := base64.RawStdEncoding.EncodeToString(nonceBytes)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'nonce-"+nonce+
		"'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusForbidden)
	if r.Method == http.MethodHead {
		return
	}
	d := challengePageData{
		Nonce: nonce, Action: ChallengeSolvePath, Token: token, Difficulty: difficulty,
		Return: safeReturnPath(r.URL.RequestURI()),
	}
	if id := r.Header.Get(api.RequestIDHeader); id != "" {
		d.Reference = " If you can't, give the site's owner this reference: " + truncateBytes(id, 128) + "."
	}
	_ = renderChallengePage(w, d)
}

// challengeCookie is the pass cookie: host-only, not readable by scripts,
// sent on same-site navigations and subresource requests.
func challengeCookie(value string, ttlSeconds int) *http.Cookie {
	return &http.Cookie{
		Name: ChallengeCookieName, Value: value, Path: "/", MaxAge: ttlSeconds,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	}
}
