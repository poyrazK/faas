package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #4 (H4-13, H4-35): `gregale usage` and `gregale domains
// list|show` named apps by UUID, and `usage` repeated the account-wide
// allowance on every app row.
func TestUsageAndDomainsNameAppsBySlug(t *testing.T) {
	resetJSONOut(t)
	const appID = "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			writeJSONTest(w, []api.AppResponse{{ID: appID, Slug: "billing-api"}})
		case "/v1/usage":
			writeJSONTest(w, []api.UsageResponse{
				{AppID: appID, Requests: 3, MBSeconds: 3_600_000, IncludedGBHours: 1500},
				{AppID: "deleted-app-id", Requests: 1, MBSeconds: 1, IncludedGBHours: 1500},
			})
		case "/v1/domains":
			writeJSONTest(w, []api.CustomDomainResponse{{Domain: "api.example.com", AppID: appID}})
		case "/v1/domains/api.example.com":
			writeJSONTest(w, api.CustomDomainResponse{Domain: "api.example.com", AppID: appID})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	if code := cmdUsageList(nil); code != 0 {
		t.Fatalf("usage exit = %d", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "billing-api — 3 · 1.000") || !strings.Contains(out, "deleted-app-id — 1") {
		t.Fatalf("usage rows must lead with the slug and fall back to the id:\n%s", out)
	}
	if strings.Count(out, "included 1500") != 1 {
		t.Fatalf("the account allowance must print exactly once:\n%s", out)
	}

	stdout.Reset()
	listCode := cmdDomains([]string{"list"})
	listOut := stdout.String()
	stdout.Reset()
	showCode := cmdDomainsShow([]string{"api.example.com"})
	if listCode != 0 || showCode != 0 {
		t.Fatalf("domains list exit=%d show exit=%d", listCode, showCode)
	}
	for name, got := range map[string]string{"list": listOut, "show": stdout.String()} {
		if !strings.Contains(got, "billing-api") || strings.Contains(got, appID) {
			t.Fatalf("domains %s must name the app by slug, not %s:\n%s", name, appID, got)
		}
	}
}
