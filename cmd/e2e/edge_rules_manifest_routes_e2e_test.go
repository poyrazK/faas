// edge_rules_manifest_routes_e2e_test.go — host and path routing declared in
// gregale.yaml (ADR-20261021223225), end to end: a project apply turns the
// manifest's routes into edge rules, the real gateways route public requests
// by them, and `routes: []` takes them away again.

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// manifestRoutesProjectTarball is a two-service project whose gregale.yaml
// carries routes as given. The Dockerfiles carry suffixes so the convention
// detector adds no workloads of its own.
func manifestRoutesProjectTarball(t *testing.T, routes string) []byte {
	t.Helper()
	const compose = `services:
  web:
    build:
      context: .
    ports:
      - "3000:3000"
  api:
    build:
      context: .
    ports:
      - "8080:8080"
`
	files := []struct{ name, body string }{
		{"shop/docker-compose.yml", compose},
		{"shop/gregale.yaml", routes},
		{"shop/Dockerfile.web", "FROM alpine:3.19\nEXPOSE 3000\nCMD [\"./web\"]\n"},
		{"shop/Dockerfile.api", "FROM alpine:3.19\nEXPOSE 8080\nCMD [\"./api\"]\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, file := range files {
		if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0o644, Size: int64(len(file.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header %s: %v", file.name, err)
		}
		if _, err := tw.Write([]byte(file.body)); err != nil {
			t.Fatalf("tar write %s: %v", file.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestE2E_EdgeRules_ManifestRoutesServePaths: a gregale.yaml route sends
// shop.example.com/api/** to the api service with /api stripped, while the
// rest of the host stays on web, the custom domain's app. Applying the
// project again with `routes: []` removes the route and its rewrite.
func TestE2E_EdgeRules_ManifestRoutesServePaths(t *testing.T) {
	f := newEdgeRulesFixture(t, "edge-manifest")
	if f == nil {
		return
	}
	const host = "shop.example.com"
	const routes = `routes:
  - name: api
    match_host: shop.example.com
    match_path: /api/**
    to: api
    strip_prefix: /api
`
	applied := applyProjectMultipart(t, f.h, f.key, "routes-shop", "", manifestRoutesProjectTarball(t, routes))
	apps := map[string]state.App{}
	for _, member := range applied.Apps {
		app, err := f.store.AppByID(f.ctx, member.ID)
		if err != nil {
			t.Fatalf("read applied app %q: %v", member.Slug, err)
		}
		apps[app.WorkloadName] = app
	}
	web, api := apps["web"], apps["api"]
	if web.ID == "" || api.ID == "" {
		t.Fatalf("apply created %+v, want web and api", applied.Apps)
	}

	// The route belongs to its target, with a rewrite for strip_prefix.
	rules, err := f.store.ListEdgeRulesForApp(f.ctx, api.ID)
	if err != nil {
		t.Fatalf("list api rules: %v", err)
	}
	owned := map[string]state.EdgeRuleKind{}
	for _, rule := range rules {
		owned[rule.ManifestKey] = rule.Kind
	}
	if owned["path-route:api"] != state.EdgeRuleKindRoute || owned["path-route:api:strip"] != state.EdgeRuleKindRewrite {
		t.Fatalf("api's manifest rules = %v, want a route and its strip rewrite", owned)
	}

	// shop.example.com is web's custom domain; both services serve.
	if _, err := f.h.Pool.Exec(f.ctx, `insert into custom_domains (domain, app_id, verified_at) values ($1, $2, now())`, host, web.ID); err != nil {
		t.Fatalf("seed custom domain: %v", err)
	}
	_, webInstance := createNormalPathLiveDeployment(t, f, web.ID, "web")
	f.vmmd.SetVersion(webInstance.ID, "web")
	_, apiInstance := createNormalPathLiveDeployment(t, f, api.ID, "api")
	f.vmmd.SetVersion(apiInstance.ID, "api")

	expectServed(t, f, host, "/api/items", "normal-path:api\n", "/items")
	expectServed(t, f, host, "/about", "normal-path:web\n", "/about")

	// routes: [] clears the manifest's routes on the next apply.
	applyProjectMultipart(t, f.h, f.key, "routes-shop", "", manifestRoutesProjectTarball(t, "routes: []\n"))
	rules, err = f.store.ListEdgeRulesForApp(f.ctx, api.ID)
	if err != nil {
		t.Fatalf("list api rules after clearing: %v", err)
	}
	for _, rule := range rules {
		if strings.HasPrefix(rule.ManifestKey, "path-route:") {
			t.Fatalf("routes: [] left %s (%s)", rule.ManifestKey, rule.Kind)
		}
	}
	expectServed(t, f, host, "/api/items", "normal-path:web\n", "/api/items")
}

// expectServed waits until a public request to host and path is answered by
// want, then checks the app received it at uri. Project apps wake and
// settle in the background, so the first request may be held or refused.
func expectServed(t *testing.T, f *normalPathFixture, host, path, want, uri string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var code int
	var body []byte
	for {
		_, body, code = doReqHeaders(t, f.h, host, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + f.key})
		if code == http.StatusOK && string(body) == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s%s: status=%d body=%q, want %q", host, path, code, body, want)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if got := f.vmmd.LastRequest().GetRequestUri(); got != uri {
		t.Errorf("GET %s%s reached the app at %q, want %q", host, path, got, uri)
	}
}
