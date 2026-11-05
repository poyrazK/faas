// adr: 20261022121642 — data-api hooks prints the migration and turns
// publication of recorded changes on and off.
package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataAPIHooksSQLRendersTheSchema(t *testing.T) {
	sql, err := main.DataAPIHooksSQL("catalog")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CREATE TABLE catalog.gregale_change_events", "ENABLE ROW LEVEL SECURITY", "CREATE FUNCTION gregale_hooks.capture()", "SECURITY DEFINER",
		"CREATE FUNCTION catalog.gregale_change_claim", "CREATE FUNCTION catalog.gregale_change_settle", "SELECT gregale_hooks.enable('catalog.orders')"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q", want)
		}
	}
	// The gateway routes only functions commented '@gregale:rpc'.
	if strings.Contains(sql, "{{schema}}") || strings.Contains(sql, "COMMENT ON FUNCTION") {
		t.Fatal("unrendered placeholder or an exposed relay function")
	}
	for _, bad := range []string{"", "API", "api;drop", "1api"} {
		if _, err := main.DataAPIHooksSQL(bad); err == nil {
			t.Fatalf("accepted schema %q", bad)
		}
	}
}

// hooksAPI fakes the app, secret, restart, Data API status and cron routes.
// A nil status answers like a control plane without the route.
type hooksAPI struct {
	mu      sync.Mutex
	secrets []api.AppSecretResponse
	writes  []string
	bodies  map[string]string
	status  *api.DataAPIStatus
	crons   []api.CronResponse
}

func newHooksClient(t *testing.T, fake *hooksAPI, base string) *api.Client {
	t.Helper()
	fake.bodies = map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodGet {
			fake.writes = append(fake.writes, r.Method+" "+r.URL.Path)
			fake.bodies[r.URL.Path] = string(body)
		}
		switch {
		case r.URL.Path == "/v1/apps/shop-data" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "8c0a63d2-5a7e-4b8f-9a55-2f8a3c1d9e01", Slug: "shop-data"})
		case r.URL.Path == "/v1/apps/shop-data/secrets":
			_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Secrets: fake.secrets})
		case strings.HasPrefix(r.URL.Path, "/v1/apps/shop-data/secrets/"):
			key := strings.TrimPrefix(r.URL.Path, "/v1/apps/shop-data/secrets/")
			if r.Method == http.MethodPut {
				fake.secrets = append(fake.secrets, api.AppSecretResponse{Key: key, Scope: "default"})
			} else {
				fake.secrets = nil
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "restart"):
			_, _ = w.Write([]byte(`{"wake_id":"wake-1"}`))
		case r.URL.Path == "/v1/apps/shop-data/data-api/status" && fake.status != nil:
			_ = json.NewEncoder(w).Encode(fake.status)
		case r.URL.Path == "/v1/crons" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(fake.crons)
		case r.URL.Path == "/v1/crons" && r.Method == http.MethodPost:
			var req api.CreateCronRequest
			_ = json.Unmarshal(body, &req)
			cron := api.CronResponse{ID: "cron-1", AppID: req.AppID, Schedule: req.Schedule, Path: req.Path, TimeoutSeconds: req.TimeoutSeconds, Enabled: true}
			fake.crons = append(fake.crons, cron)
			_ = json.NewEncoder(w).Encode(cron)
		case strings.HasPrefix(r.URL.Path, "/v1/crons/") && r.Method == http.MethodPatch:
			var req api.UpdateCronRequest
			_ = json.Unmarshal(body, &req)
			fake.crons[0].Schedule = *req.Schedule
			_ = json.NewEncoder(w).Encode(fake.crons[0])
		case strings.HasPrefix(r.URL.Path, "/v1/crons/") && r.Method == http.MethodDelete:
			fake.crons = nil
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := api.NewClient(base, "test-token")
	client.HTTPClient().Transport = hooksTransport(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})
	return client
}

type hooksTransport func(*http.Request) (*http.Response, error)

func (f hooksTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDataAPIHooksEnableStoresTheOriginAndReportsTheSource(t *testing.T) {
	fake := &hooksAPI{}
	client := newHooksClient(t, fake, "https://api.gregale.example")
	ctx := context.Background()
	receipt, err := main.EnableDataAPIHooks(ctx, client, "shop-data", api.DefaultEnvScope, true)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Enabled || !receipt.Restarted || receipt.Source != "app.8c0a63d2-5a7e-4b8f-9a55-2f8a3c1d9e01" {
		t.Fatalf("receipt %+v", receipt)
	}
	if got := strings.Join(fake.writes, "|"); got != "PUT /v1/apps/shop-data/secrets/DATA_API_CHANGE_EVENTS_URL|POST /v1/apps/shop-data/restart" {
		t.Fatalf("writes %s", got)
	}
	if !strings.Contains(fake.bodies["/v1/apps/shop-data/secrets/DATA_API_CHANGE_EVENTS_URL"], "https://api.gregale.example/") {
		t.Fatalf("stored %s", fake.bodies["/v1/apps/shop-data/secrets/DATA_API_CHANGE_EVENTS_URL"])
	}
	status, err := main.DataAPIHooksStatus(ctx, client, "shop-data", api.DefaultEnvScope)
	if err != nil || !status.Enabled {
		t.Fatalf("status %+v %v", status, err)
	}
	fake.writes = nil
	off, err := main.DisableDataAPIHooks(ctx, client, "shop-data", api.DefaultEnvScope, false)
	if err != nil || off.Enabled || off.Restarted || len(fake.writes) != 1 || !strings.HasPrefix(fake.writes[0], "DELETE /v1/apps/shop-data/secrets/DATA_API_CHANGE_EVENTS_URL") {
		t.Fatalf("disable %+v %v %v", off, err, fake.writes)
	}
}

func TestDataAPIHooksNeedAnHTTPSPlatform(t *testing.T) {
	fake := &hooksAPI{}
	client := newHooksClient(t, fake, "http://localhost:8080")
	if _, err := main.EnableDataAPIHooks(context.Background(), client, "shop-data", api.DefaultEnvScope, true); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("err %v", err)
	}
	if len(fake.writes) != 0 {
		t.Fatalf("wrote %v", fake.writes)
	}
}

// Status reports what the runtime last reported, without waking the API, and
// says plainly when the installation accepts no runtime reports.
func TestDataAPIHooksStatusReportsDelivery(t *testing.T) {
	oldest := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	fake := &hooksAPI{secrets: []api.AppSecretResponse{{Key: "DATA_API_CHANGE_EVENTS_URL", Scope: "default"}},
		crons: []api.CronResponse{{ID: "cron-1", Path: api.DataAPIChangeHooksDrainPath, Schedule: "*/5 * * * *", Enabled: true}},
		status: &api.DataAPIStatus{RuntimeVersion: "0123456789abcdef", RuntimeReports: api.DataAPIRuntimeReportsAvailable, ChangeHooks: &api.DataAPIChangeHookStatus{
			ReportedAt: oldest.Add(time.Minute), Pending: 3, Failed: 1, OldestPendingAt: &oldest, LastFailureCode: "event_schema_invalid", Publishing: api.DataAPIChangeHooksPublishingRetrying}}}
	client := newHooksClient(t, fake, "https://api.gregale.example")
	receipt, err := main.DataAPIHooksStatus(context.Background(), client, "shop-data", api.DefaultEnvScope)
	if err != nil || receipt.DrainEvery != "5m" || receipt.Delivery == nil || receipt.Delivery.Pending != 3 {
		t.Fatalf("status %+v %v", receipt, err)
	}
	line := main.DataAPIHooksDelivery(receipt)
	for _, want := range []string{"3 waiting (oldest recorded 2026-11-02T12:00:00Z)", "1 rejected (event_schema_invalid", "gregale_hooks.retry_failed()", "failing and retrying"} {
		if !strings.Contains(line, want) {
			t.Fatalf("delivery %q lacks %q", line, want)
		}
	}
	fake.status.RuntimeReports, fake.status.ChangeHooks = api.DataAPIRuntimeReportsUnavailable, nil
	receipt, _ = main.DataAPIHooksStatus(context.Background(), client, "shop-data", api.DefaultEnvScope)
	if line := main.DataAPIHooksDelivery(receipt); !strings.Contains(line, "workload identity is not configured") {
		t.Fatalf("delivery without runtime reports %q", line)
	}
	fake.status = nil
	receipt, err = main.DataAPIHooksStatus(context.Background(), client, "shop-data", api.DefaultEnvScope)
	if err != nil || receipt.RuntimeReports != "" || receipt.Delivery != nil {
		t.Fatalf("an older control plane %+v %v", receipt, err)
	}
}

// Monthly caps count on platform totals that only runtime reports bring, so
// an installation that accepts none refuses them; usage reporting is stored
// with a warning, and an older control plane is not second-guessed.
func TestDataAPIMonthlyCapsNeedRuntimeReports(t *testing.T) {
	fake := &hooksAPI{status: &api.DataAPIStatus{RuntimeVersion: "0123456789abcdef", RuntimeReports: api.DataAPIRuntimeReportsUnavailable}}
	client := newHooksClient(t, fake, "https://api.gregale.example")
	ctx := context.Background()
	if err := main.CheckDataAPIReportSettings(ctx, client, "shop-data", "requests=5", ""); err == nil || !strings.Contains(err.Error(), "workload identity is not configured") {
		t.Fatalf("caps without runtime reports: %v", err)
	}
	for _, tc := range []struct{ caps, reporting string }{{"", "on"}, {"off", ""}, {"", "off"}} {
		if err := main.CheckDataAPIReportSettings(ctx, client, "shop-data", tc.caps, tc.reporting); err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	fake.status.RuntimeReports = api.DataAPIRuntimeReportsAvailable
	if err := main.CheckDataAPIReportSettings(ctx, client, "shop-data", "requests=5", ""); err != nil {
		t.Fatalf("caps with runtime reports: %v", err)
	}
	fake.status = nil
	if err := main.CheckDataAPIReportSettings(ctx, client, "shop-data", "requests=5", ""); err != nil {
		t.Fatalf("an older control plane: %v", err)
	}
	if len(fake.writes) != 0 {
		t.Fatalf("the check wrote %v", fake.writes)
	}
}

// A drain schedule is one cron on the drain path; enable creates or changes
// it, off and disable remove it.
func TestDataAPIHooksDrainSchedule(t *testing.T) {
	fake := &hooksAPI{}
	client := newHooksClient(t, fake, "https://api.gregale.example")
	t.Setenv("FAAS_API", "https://api.gregale.example")
	ctx := context.Background()
	receipt, err := main.EnableDataAPIHooks(ctx, client, "shop-data", api.DefaultEnvScope, false)
	if err == nil {
		err = main.ApplyDataAPIDrainSchedule(ctx, client, &receipt, "*/5 * * * *", "5m")
	}
	if err != nil || receipt.DrainEvery != "5m" || len(fake.crons) != 1 {
		t.Fatalf("enable %+v %v %+v", receipt, err, fake.crons)
	}
	if cron := fake.crons[0]; cron.Path != api.DataAPIChangeHooksDrainPath || cron.AppID != "8c0a63d2-5a7e-4b8f-9a55-2f8a3c1d9e01" || cron.TimeoutSeconds != int(api.DataAPIChangeHooksDrainCronTimeout/time.Second) {
		t.Fatalf("cron %+v", cron)
	}
	if err := main.ApplyDataAPIDrainSchedule(ctx, client, &receipt, "0 * * * *", "1h"); err != nil || len(fake.crons) != 1 || fake.crons[0].Schedule != "0 * * * *" {
		t.Fatalf("change %v %+v", err, fake.crons)
	}
	off, err := main.DisableDataAPIHooks(ctx, client, "shop-data", api.DefaultEnvScope, false)
	if err != nil || off.DrainEvery != "" || len(fake.crons) != 0 {
		t.Fatalf("disable %+v %v %+v", off, err, fake.crons)
	}
	for every, want := range map[string]string{"1m": "* * * * *", "15m": "*/15 * * * *", "1h": "0 * * * *", "6h": "0 */6 * * *", "24h": "0 0 * * *"} {
		if got, err := main.DataAPIDrainSchedule(every); err != nil || got != want || main.DataAPIDrainEvery(got) != every {
			t.Fatalf("%s = %q %v", every, got, err)
		}
	}
	for _, bad := range []string{"7m", "90s", "5h", "48h", "0m", "soon"} {
		if _, err := main.DataAPIDrainSchedule(bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

func TestDataAPIHooksUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"publish"}, {"enable"}, {"enable", "Bad Name"}, {"status", "shop-data", "--no-restart"}, {"sql", "extra"},
		{"status", "shop-data", "--drain-every", "5m"}, {"enable", "shop-data", "--drain-every", "7m"}, {"enable", "shop-data", "--drain-every", "5m", "--scope", "preview"}} {
		_, _, restore := main.SwapIO(t)
		code := main.CmdDataAPIHooks(args)
		restore()
		if code == 0 {
			t.Fatalf("%v accepted", args)
		}
	}
}
