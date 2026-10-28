package journeys

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPublicSurface covers what a visitor reaches without an account:
// API health, the OpenAPI spec, the status API, edge routing for unknown
// hosts, security.txt and the website (inventory rows 141, 159, 161).
func TestPublicSurface(t *testing.T) {
	tg := needs(t, tierSmoke, false)
	t.Parallel()
	site := "https://" + tg.appsDomain

	t.Run("api_health", func(t *testing.T) {
		r := get(tg.api + "/healthz")
		if r.status != http.StatusOK || str(jsonBody(t, r), "status") != "ok" {
			t.Fatalf("GET /healthz: %s", r)
		}
	})
	t.Run("openapi_spec", func(t *testing.T) {
		r := get(tg.api + "/v1/openapi.json")
		if r.status != http.StatusOK {
			t.Fatalf("GET /v1/openapi.json: %s", r)
		}
		var spec struct {
			OpenAPI string                     `json:"openapi"`
			Paths   map[string]json.RawMessage `json:"paths"`
		}
		if err := json.Unmarshal(r.body, &spec); err != nil {
			t.Fatalf("decode OpenAPI: %v", err)
		}
		if !strings.HasPrefix(spec.OpenAPI, "3.") || len(spec.Paths) < 100 {
			t.Fatalf("OpenAPI spec: version %q with %d paths", spec.OpenAPI, len(spec.Paths))
		}
	})
	t.Run("status_api", func(t *testing.T) {
		r := get(tg.api + "/v1/status")
		if r.status != http.StatusOK {
			t.Fatalf("GET /v1/status: %s", r)
		}
		if s := str(jsonBody(t, r), "overall_status"); s == "" {
			t.Fatalf("status API has no overall_status: %s", r)
		}
	})
	t.Run("status_incidents", func(t *testing.T) {
		r := get(tg.api + "/v1/status/incidents")
		if r.status != http.StatusOK {
			t.Fatalf("GET /v1/status/incidents: %s", r)
		}
		jsonBody(t, r)
	})
	t.Run("cli_status", func(t *testing.T) {
		res := tg.runAs(t, "", time.Minute, "status")
		if res.exit != 0 {
			t.Fatalf("gregale status: %s", res.problem())
		}
		if field(object(t, res.stdout), "api_availability_pct") == nil {
			t.Fatalf("gregale status has no api_availability_pct: %s", tail(string(res.stdout), 500))
		}
	})
	t.Run("unknown_app_host_404", func(t *testing.T) {
		r := get(tg.appURL(tg.slug("absent")) + "/")
		if r.status != http.StatusNotFound || str(jsonBody(t, r), "code") != "not_found" {
			t.Fatalf("unrouted app host: %s", r)
		}
	})
	t.Run("website_and_status_page", func(t *testing.T) {
		for _, path := range []string{"/", "/docs", "/status"} {
			if r := getFollow(site + path); r.status != http.StatusOK {
				t.Errorf("GET %s%s: %s", site, path, r)
			}
		}
	})
	t.Run("security_txt", func(t *testing.T) {
		r := get(tg.api + "/.well-known/security.txt")
		if r.status != http.StatusOK {
			t.Fatalf("GET security.txt: %s", r)
		}
		fields := securityTxtFields(string(r.body))
		if len(fields["contact"]) == 0 {
			t.Errorf("security.txt has no Contact")
		}
		if exp, err := time.Parse(time.RFC3339, strings.ToUpper(first(fields["expires"]))); err != nil || exp.Before(time.Now()) {
			t.Errorf("security.txt Expires %q is missing or past", first(fields["expires"]))
		}
		// RFC 9116 §2.5.2: the Canonical URI must serve this file.
		for _, canonical := range fields["canonical"] {
			c := getFollow(canonical)
			if c.status != http.StatusOK || !strings.Contains(string(c.body), "Contact:") {
				t.Errorf("Canonical %s does not serve security.txt: %s", canonical, c)
			}
		}
		// The Encryption URI must point at a key, not an HTML page.
		for _, enc := range fields["encryption"] {
			e := getFollow(enc)
			if e.status != http.StatusOK || !strings.Contains(string(e.body), "BEGIN PGP PUBLIC KEY BLOCK") {
				t.Errorf("Encryption %s does not serve a PGP public key: HTTP %d %s", enc, e.status, e.header.Get("Content-Type"))
			}
		}
	})
}

// TestDocsPages checks every page in docs/customer-pages.json answers 200,
// so a renamed doc cannot leave a dead link in the CLI or dashboard. A route
// marked unpublished (a preview or internal capability's page, with the
// reason) is deliberately not on the site and is skipped (#5709).
func TestDocsPages(t *testing.T) {
	tg := needs(t, tierSmoke, false)
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "customer-pages.json"))
	if err != nil {
		t.Fatalf("read docs/customer-pages.json: %v", err)
	}
	var pages struct {
		Routes []struct {
			Path        string `json:"path"`
			External    bool   `json:"external"`
			Unpublished string `json:"unpublished"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &pages); err != nil {
		t.Fatalf("decode docs/customer-pages.json: %v", err)
	}
	base := "https://" + tg.appsDomain + "/docs"
	var mu sync.Mutex
	var failed []string
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	checked, unpublished := 0, 0
	for _, route := range pages.Routes {
		if route.External {
			continue
		}
		if route.Unpublished != "" {
			unpublished++
			continue
		}
		checked++
		pageURL := base
		if route.Path != "" {
			pageURL += "/" + route.Path
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if r := getFollow(pageURL); r.status != http.StatusOK {
				mu.Lock()
				failed = append(failed, pageURL+": "+r.String())
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	t.Logf("checked %d docs pages; %d unpublished by design", checked, unpublished)
	for _, f := range failed {
		t.Error(f)
	}
}

// getFollow is get with redirects followed (up to 10), for pages a
// browser would reach.
func getFollow(rawURL string) httpResult {
	for range 10 {
		r := get(rawURL)
		if r.err != nil || r.status < 300 || r.status > 399 {
			return r
		}
		next, err := url.Parse(r.header.Get("Location"))
		if err != nil {
			return r
		}
		base, _ := url.Parse(rawURL)
		rawURL = base.ResolveReference(next).String()
	}
	return httpResult{err: errors.New("more than 10 redirects")}
}

func securityTxtFields(body string) map[string][]string {
	fields := map[string][]string{}
	for _, line := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		fields[key] = append(fields[key], strings.TrimSpace(v))
	}
	return fields
}

func first(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}
