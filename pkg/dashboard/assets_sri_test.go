package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

// TestExternalScriptsAreSRI protects the dashboard's CSP boundary. A nonce
// authorizes the tag in this response, but it does not protect the browser
// from a compromised CDN response. Every external script must carry the exact
// digest for its version and use an anonymous CORS fetch so the browser
// performs the integrity check.
func TestExternalScriptsAreSRI(t *testing.T) {
	wantIntegrity := map[string]string{
		"https://unpkg.com/htmx.org@2.0.4":     "sha384-HGfztofotfshcF7+8n44JQL2oJmowVChPTg48S+jvZoztPfvwD79OC/LTtG6dMp+",
		"https://unpkg.com/htmx-ext-sse@2.2.2": "sha384-fw+eTlCc7suMV/1w/7fr2/PmwElUIt5i82bi+qTiLXvjRXZ2/FkiTNA/w0MhXnGI",
	}
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
			if !strings.HasPrefix(src, "https://") {
				continue
			}
			want, ok := wantIntegrity[src]
			if !ok {
				t.Errorf("%s: external script %s is not in the pinned asset set", entry.Name(), src)
				continue
			}
			attrs := map[string]string{}
			for _, attr := range attrRE.FindAllStringSubmatch(match[0], -1) {
				attrs[attr[1]] = attr[2]
			}
			if got := attrs["integrity"]; got != want {
				t.Errorf("%s: %s integrity = %q, want %q", entry.Name(), src, got, want)
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
