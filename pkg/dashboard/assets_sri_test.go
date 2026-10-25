package dashboard

import (
	"crypto/sha512"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// pinnedScripts is every script the dashboard loads, keyed by the path its
// templates reference, with the SRI digest the tag must carry.
var pinnedScripts = map[string]struct{ file, integrity string }{
	HTMXScriptPath:    {"assets/htmx-2.0.4.min.js", "sha384-HGfztofotfshcF7+8n44JQL2oJmowVChPTg48S+jvZoztPfvwD79OC/LTtG6dMp+"},
	HTMXSSEScriptPath: {"assets/htmx-ext-sse-2.2.2.js", "sha384-fw+eTlCc7suMV/1w/7fr2/PmwElUIt5i82bi+qTiLXvjRXZ2/FkiTNA/w0MhXnGI"},
}

// TestScriptsAreSelfHostedAndSRI protects the dashboard's CSP boundary. The
// policy allows scripts from 'self' and the response nonce only, so every
// template script must be one of the vendored files, carry its exact digest
// and the nonce. A third-party src would be refused by the browser anyway;
// failing here names the template.
func TestScriptsAreSelfHostedAndSRI(t *testing.T) {
	scriptRE := regexp.MustCompile(`<script\b[^>]*src="([^"]+)"[^>]*>`)
	attrRE := regexp.MustCompile(`\b(integrity|crossorigin|nonce)="([^"]*)"`)

	entries, err := tmplFS.ReadDir("templates")
	if err != nil {
		t.Fatalf("read embedded dashboard templates: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		raw, err := tmplFS.ReadFile("templates/" + entry.Name())
		if err != nil {
			t.Fatalf("read embedded template %s: %v", entry.Name(), err)
		}
		for _, match := range scriptRE.FindAllStringSubmatch(string(raw), -1) {
			src := match[1]
			pinned, ok := pinnedScripts[src]
			if !ok {
				t.Errorf("%s: script %s is not a vendored dashboard asset", entry.Name(), src)
				continue
			}
			attrs := map[string]string{}
			for _, attr := range attrRE.FindAllStringSubmatch(match[0], -1) {
				attrs[attr[1]] = attr[2]
			}
			if got := attrs["integrity"]; got != pinned.integrity {
				t.Errorf("%s: %s integrity = %q, want %q", entry.Name(), src, got, pinned.integrity)
			}
			if got := attrs["crossorigin"]; got != "anonymous" {
				t.Errorf("%s: %s crossorigin = %q, want anonymous", entry.Name(), src, got)
			}
			if got := attrs["nonce"]; got != "{{.Nonce}}" {
				t.Errorf("%s: %s nonce = %q, want {{.Nonce}}", entry.Name(), src, got)
			}
		}
	}
}

// The vendored bytes must be the release the SRI digest names, or browsers
// refuse the script and every htmx interaction stops.
func TestVendoredAssetsMatchTheirIntegrity(t *testing.T) {
	for path, pinned := range pinnedScripts {
		body, err := assetFS.ReadFile(pinned.file)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha512.Sum384(body)
		if got := "sha384-" + base64.StdEncoding.EncodeToString(sum[:]); got != pinned.integrity {
			t.Errorf("%s digest = %s, want %s", path, got, pinned.integrity)
		}
	}
}

func TestServeAssetAnswersOnlyVendoredScripts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dashboard/assets/{file}", ServeAsset)
	for path := range pinnedScripts {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || rec.Body.Len() == 0 {
			t.Errorf("%s = %d %q (%d bytes)", path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
	}
	for _, path := range []string{"/dashboard/assets/README.md", "/dashboard/assets/assets.go", "/dashboard/assets/..%2Fdashboard.go"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
}
