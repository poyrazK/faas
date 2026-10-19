// adr: 20261019102108 — usage reporting settings, caps and the usage view.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataAPIUsageURLAcceptsOnlyHTTPSOrigins(t *testing.T) {
	for base, want := range map[string]string{
		"https://api.gregale.dev":      "https://api.gregale.dev/",
		"https://api.gregale.dev/":     "https://api.gregale.dev/",
		"https://api.example:8443":     "https://api.example:8443/",
		"http://127.0.0.1:8080":        "",
		"https://api.gregale.dev/v1":   "",
		"https://u:p@api.gregale.dev":  "",
		"https://api.gregale.dev/?x=1": "",
	} {
		got, ok := dataAPIUsageURL(base)
		if got != want || ok != (want != "") {
			t.Fatalf("%s: got %q %v", base, got, ok)
		}
	}
}

func TestParseDataAPIMonthlyCaps(t *testing.T) {
	caps, err := parseDataAPIMonthlyCaps("requests=1000000,anonymous=50000")
	if err != nil || *caps.Requests != 1000000 || *caps.AnonymousRequests != 50000 {
		t.Fatalf("caps = %+v (%v)", caps, err)
	}
	encoded, _ := json.Marshal(caps)
	if string(encoded) != `{"version":1,"requests":1000000,"anonymous_requests":50000}` {
		t.Fatalf("runtime contract = %s", encoded)
	}
	only, _ := parseDataAPIMonthlyCaps("anonymous=10")
	if encoded, _ = json.Marshal(only); string(encoded) != `{"version":1,"anonymous_requests":10}` {
		t.Fatalf("anonymous only = %s", encoded)
	}
	for _, bad := range []string{"", "requests", "requests=0", "requests=-1", "requests=x", "rows=5", "requests=1,requests=2", "requests=1000000000001"} {
		if _, err := parseDataAPIMonthlyCaps(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// httpsClient talks to the settings fake while reporting an HTTPS base URL.
func httpsClient(t *testing.T) *api.Client {
	t.Helper()
	target, err := url.Parse(os.Getenv("FAAS_API"))
	if err != nil {
		t.Fatal(err)
	}
	client := api.NewClient("https://api.gregale.example", "test-token")
	client.HTTPClient().Transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})
	return client
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConfigureDataAPIUsageStoresTheURLBeforeCapsAndRemovesItAfter(t *testing.T) {
	server := newSettingsAPI(t)
	receipt := dataAPIConfigureReceipt{Changed: []string{}}
	if err := configureDataAPIUsage(context.Background(), httpsClient(t), "shop-data", api.DefaultEnvScope, "", "requests=100,anonymous=10", &receipt); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(server.calls(), "|"); got != "PUT /v1/apps/shop-data/secrets/DATA_API_USAGE_URL|PUT /v1/apps/shop-data/secrets/DATA_API_MONTHLY_CAPS" {
		t.Fatalf("calls = %s", got)
	}
	if !strings.Contains(server.bodyFor("/DATA_API_USAGE_URL"), "https://api.gregale.example/") || !strings.Contains(server.bodyFor("/DATA_API_MONTHLY_CAPS"), `{\"version\":1,\"requests\":100,\"anonymous_requests\":10}`) {
		t.Fatalf("bodies: %s %s", server.bodyFor("/DATA_API_USAGE_URL"), server.bodyFor("/DATA_API_MONTHLY_CAPS"))
	}
	if strings.Join(receipt.Changed, ",") != "monthly_caps" || *receipt.MonthlyCaps.AnonymousRequests != 10 {
		t.Fatalf("receipt = %+v", receipt)
	}
	off := newSettingsAPI(t)
	if err := configureDataAPIUsage(context.Background(), httpsClient(t), "shop-data", api.DefaultEnvScope, "off", "", &dataAPIConfigureReceipt{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(off.calls(), "|"); got != "DELETE /v1/apps/shop-data/secrets/DATA_API_MONTHLY_CAPS|DELETE /v1/apps/shop-data/secrets/DATA_API_USAGE_URL" {
		t.Fatalf("off calls = %s (caps must go before the URL they need)", got)
	}
}

func TestDataAPIConfigureRefusesCapsWithoutAnHTTPSAPI(t *testing.T) {
	resetJSONOut(t)
	server := newSettingsAPI(t)
	if code := cmdDataAPIConfigure([]string{"shop-data", "--monthly-caps", "requests=100", "--no-restart"}); code == 0 {
		t.Fatal("caps stored for a runtime that could not report")
	}
	if calls := server.calls(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
	for _, args := range [][]string{
		{"shop-data", "--monthly-caps", "requests=0"},
		{"shop-data", "--usage-reporting", "maybe"},
		{"shop-data", "--usage-reporting", "off", "--monthly-caps", "requests=5"},
	} {
		if code := cmdDataAPIConfigure(args); code == 0 {
			t.Fatalf("%v accepted", args)
		}
	}
	if code := cmdDataAPIConfigure([]string{"shop-data", "--usage-reporting", "off", "--monthly-caps", "off", "--no-restart"}); code != 0 {
		t.Fatal("turning reporting and caps off must work on any API")
	}
	if got := strings.Join(server.calls(), "|"); got != "DELETE /v1/apps/shop-data/secrets/DATA_API_MONTHLY_CAPS|DELETE /v1/apps/shop-data/secrets/DATA_API_USAGE_URL" {
		t.Fatalf("calls = %s", got)
	}
}

func TestDataAPIUsageCommandPrintsTotalsAndBuckets(t *testing.T) {
	resetJSONOut(t)
	start := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	usage := api.DataAPIUsage{App: "shop-data", Granularity: "day", From: start, To: start.Add(48 * time.Hour),
		Totals:      api.DataAPIUsageCounts{UserRequests: 10, AnonymousRequests: 5, ServerRequests: 1, RejectedRequests: 2, CappedRequests: 3, RowsReturned: 70, ResponseBytes: 2048},
		MonthToDate: api.DataAPIUsageMonthToDate{Month: "2026-10", Requests: 400, AnonymousRequests: 90},
		Buckets: []api.DataAPIUsageBucket{{Start: start, DataAPIUsageCounts: api.DataAPIUsageCounts{UserRequests: 10, RowsReturned: 70}},
			{Start: start.Add(24 * time.Hour), DataAPIUsageCounts: api.DataAPIUsageCounts{AnonymousRequests: 5, ServerRequests: 1, CappedRequests: 3}}}}
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/shop-data/data-api/usage" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(usage)
	}))
	t.Cleanup(server.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_API", server.URL)
	out, restore := captureStdout(t)
	defer restore()
	if code := cmdDataAPIUsage([]string{"shop-data", "--since", "2d"}); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	text := out.String()
	for _, want := range []string{"last 2d (UTC days)", "Requests: 16 (user 10, anonymous 5, server 1); rejected 2, errors 0, refused by caps 3", "This month (2026-10): 400 requests, 90 anonymous", "2026-10-19", "START"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if query != "since=2d" {
		t.Fatalf("query = %q", query)
	}
	if code := cmdDataAPIUsage([]string{"shop-data", "--granularity", "week"}); code != 1 {
		t.Fatal("invalid granularity accepted")
	}
}

func TestDataAPIMonthlyCapDiagnostics(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("", "")
	c.observe(dataAPIDiagnosticFixture(t, 1, "rest", "rejected", 429, 1, "data_api_monthly_cap_reached"))
	c.finish()
	if c.report.Failures.MonthlyCapReached != 1 {
		t.Fatalf("caps were misclassified: %+v", c.report.Failures)
	}
	stdout, _, restore := swapIO(t)
	defer restore()
	if err := writeDataAPIDiagnostics(c.report); err != nil || !strings.Contains(stdout.String(), "Monthly caps: 1 requests refused") {
		t.Fatalf("missing cap text: %s (%v)", stdout.String(), err)
	}
}
