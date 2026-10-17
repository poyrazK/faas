package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func dataAPIHealthFixture(ready bool, refresh string, active int) string {
	database, sql := "reachable", "ready"
	capacity := 2
	if refresh != "disabled" {
		capacity = 1
	}
	if !ready {
		sql = "unready"
		if refresh != "active" && refresh != "paused" {
			database = "unknown"
		}
	}
	return fmt.Sprintf(`{"version":1,"live":true,"ready":%t,"checks":{"engine":"reachable","sql":%q,"database":%q,"refresh":%q},"admission":{"active":%d,"queued":0,"capacity":%d,"queue_capacity":8},"pool":%s,"private":"private-response-secret"}`, ready, sql, database, refresh, active, capacity, dataAPIHealthPoolFixture(capacity))
}

func dataAPIHealthPoolFixture(capacity int) string {
	return fmt.Sprintf(`{"state":"observed","checked_at":"2026-10-16T10:00:00Z","capacity":%d,"available":0,"waiting":0,"timeouts_total":0,"private":"private-response-secret"}`, capacity)
}

func TestDataAPIHealthReportSeparatesCurrentChecksAndRecentFailures(t *testing.T) {
	for _, scenario := range []string{"healthy", "saturated", "paused", "unready", "engine-exit", "legacy", "redirect", "partial-logs", "logs-unavailable", "deadline", "pool-waiting", "pool-timeouts", "pool-missing", "pool-invalid", "pool-unknown", "budget-unknown", "budget-exceeded"} {
		t.Run(scenario, func(t *testing.T) {
			var redirected, reads atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
			defer target.Close()
			serving := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Cache-Control") != "no-cache" {
					t.Error("health probes must be read-only and carry no credentials")
				}
				if scenario == "redirect" {
					http.Redirect(w, r, target.URL, http.StatusFound)
					return
				}
				if r.URL.Path == "/livez" {
					if scenario == "legacy" {
						http.NotFound(w, r)
						return
					}
					_, _ = fmt.Fprint(w, `{"version":1,"live":true,"private":"private-response-secret"}`)
					return
				}
				if r.URL.Path != "/healthz" {
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
				ready, refresh, active := true, "disabled", 0
				switch scenario {
				case "legacy":
					_, _ = fmt.Fprint(w, `{"ready":true}`)
					return
				case "saturated":
					ready, active = false, 2
				case "paused":
					refresh = "paused"
				case "unready", "engine-exit":
					ready = false
				}
				if !ready {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				body := dataAPIHealthFixture(ready, refresh, active)
				switch scenario {
				case "pool-waiting":
					body = strings.ReplaceAll(body, `"waiting":0`, `"waiting":1`)
				case "pool-timeouts":
					body = strings.ReplaceAll(body, `"timeouts_total":0`, `"timeouts_total":9`)
				case "pool-missing":
					body = strings.ReplaceAll(body, `,"pool":`+dataAPIHealthPoolFixture(api.DataAPIMaxPoolConnections), ``)
				case "pool-invalid":
					body = strings.ReplaceAll(body, `"waiting":0`, `"waiting":"private-response-secret"`)
				case "pool-unknown":
					body = strings.ReplaceAll(body, dataAPIHealthPoolFixture(api.DataAPIMaxPoolConnections), `{"state":"unknown","reason":"timeout","checked_at":"2026-10-16T10:00:00Z"}`)
				}
				if scenario == "engine-exit" {
					body = strings.ReplaceAll(strings.ReplaceAll(body, `"engine":"reachable"`, `"engine":"unreachable"`), `"sql":"unready"`, `"sql":"unknown"`)
				}
				_, _ = fmt.Fprint(w, body)
			}))
			defer serving.Close()
			original := http.DefaultTransport
			http.DefaultTransport = serving.Client().Transport
			defer func() { http.DefaultTransport = original }()
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer owner-test-token" {
					t.Error("only authorized management reads are permitted")
				}
				switch r.URL.Path {
				case "/v1/apps/notes/data-api/connection-budget":
					if scenario == "budget-unknown" || scenario == "budget-exceeded" {
						value := connectionBudgetFixture()
						if scenario == "budget-unknown" {
							value.State = "unknown"
							value.Databases[0].State = "unknown"
							value.Databases[0].Capacity = api.ConnectionBudgetCapacity{State: "unknown"}
							value.Databases[0].Reasons = []string{"capacity_unverified"}
						} else {
							value.State = "over_budget"
							value.Databases[0].State = "over_budget"
							*value.Databases[0].Capacity.UsableConnections = 6
						}
						writeJSONTest(w, value)
						return
					}
					writeJSONTest(w, api.DataAPIConnectionBudgets{Version: 1, App: "notes", State: "not_applicable", Databases: []api.ManagedPostgresConnectionBudget{}})
				case "/v1/apps/notes":
					writeJSONTest(w, api.AppResponse{Slug: "notes", CanonicalURL: serving.URL})
				case "/v1/apps/notes/logs":
					if scenario == "logs-unavailable" {
						http.Error(w, "private-stream-secret", http.StatusForbidden)
						return
					}
					if scenario == "deadline" {
						<-r.Context().Done()
						return
					}
					if r.URL.Query().Get("follow") == "true" || r.URL.Query().Get("since") == "" {
						t.Error("health must use a bounded recent sample")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if scenario == "partial-logs" {
						_, _ = fmt.Fprint(w, "event: degraded\ndata: {\"private\":\"private-stream-secret\"}\n\n")
						return
					}
					for i, code := range []string{"data_api_overloaded", "query_timeout", "data_api_refresh_paused", "data_api_refresh_unavailable", "response_too_large", "data_api_rate_limited"} {
						outcome := "unavailable"
						if i == 0 {
							outcome = "overloaded"
						}
						if i == 1 {
							outcome = "query_timeout"
						}
						if i == 2 {
							outcome = "rejected"
						}
						line := dataAPIDiagnosticFixture(t, i+1, "rest", outcome, 503, 10, code)
						if code == "response_too_large" {
							metadata := fmt.Sprintf(`{"source":"content_length","limit_bytes":%d,"observed_at_least_bytes":%d}`, api.DataAPIMaxResponseBytes, api.DataAPIMaxResponseBytes+1)
							line = responseLimitDiagnosticFixture(t, i+1, metadata, false)
						}
						if code == "data_api_rate_limited" {
							line = rateLimitDiagnosticFixture(t, i+1, `{"reason":"subject","per_second":10,"burst":20,"retry_after_seconds":1}`)
						}
						line = strings.ReplaceAll(line, "2026-10-11T10:00:00Z", time.Now().UTC().Format(time.RFC3339Nano))
						_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", dataAPIDiagnosticEnvelope(t, line))
					}
					_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
				default:
					t.Errorf("unexpected management path %s", r.URL.Path)
				}
			}))
			defer management.Close()
			t.Setenv("FAAS_API", management.URL)
			t.Setenv("FAAS_TOKEN", "owner-test-token")
			stdout, stderr, restore := swapIO(t)
			defer restore()
			args := []string{"--json", "data-api", "health", "notes", "--since", "15m"}
			if scenario == "deadline" {
				args = append(args, "--timeout", "250ms")
			}
			code := run(args)
			var report dataAPIHealthReport
			if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
				t.Fatalf("report: %v, stdout=%s stderr=%s", err, stdout.String(), stderr())
			}
			if (code == 0) != (scenario == "healthy" || scenario == "pool-timeouts") || reads.Load() != 2 || redirected.Load() != 0 {
				t.Fatalf("exit=%d reads=%d redirects=%d report=%+v", code, reads.Load(), redirected.Load(), report)
			}
			if scenario == "healthy" && (report.Status != "healthy" || len(report.RecentFindings) != 7 || report.Diagnostics.Failures.RefreshPaused != 1 || report.Diagnostics.Failures.RefreshUnavailable != 1 || report.Diagnostics.Failures.ResponseTooLarge != 1 || report.Diagnostics.Failures.RateLimited != 1 || !slices.Contains(report.RecentFindings, "response_limits_observed") || !slices.Contains(report.RecentFindings, "rate_limits_observed")) {
				t.Fatalf("recent evidence changed current health: %+v", report)
			}
			if scenario == "budget-unknown" && (report.Status != "healthy" || !report.Partial || report.ConnectionBudget == nil || report.ConnectionBudget.State != "unknown") {
				t.Fatal("unknown budget changed valid SQL readiness", report)
			}
			if scenario == "budget-exceeded" && (report.Status != "degraded" || !slices.Contains(report.CurrentFindings, "connection_budget_exceeded") || report.Readiness.Ready == nil || !*report.Readiness.Ready) {
				t.Fatal("budget comparison discarded SQL readiness", report)
			}
			for scenarioName, finding := range map[string]string{"saturated": "admission_saturated", "paused": "refresh_paused", "unready": "database_connectivity_unknown", "engine-exit": "engine_unreachable"} {
				if scenario == scenarioName && !slices.Contains(report.CurrentFindings, finding) {
					t.Fatalf("missing %s: %+v", finding, report)
				}
			}
			if (scenario == "legacy" || scenario == "redirect" || scenario == "partial-logs" || scenario == "logs-unavailable" || scenario == "deadline") && !report.Partial {
				t.Fatal("missing coverage must stay partial")
			}
			if strings.HasPrefix(scenario, "pool-") {
				if report.Readiness.State != "ok" || report.Readiness.Ready == nil || !*report.Readiness.Ready {
					t.Fatal("pool diagnostics discarded valid readiness")
				}
				partial := scenario == "pool-missing" || scenario == "pool-invalid" || scenario == "pool-unknown"
				if report.Partial != partial || slices.Contains(report.CurrentFindings, "pool_waiting") != (scenario == "pool-waiting") {
					t.Fatalf("pool evidence changed health incorrectly: %+v", report)
				}
				if scenario == "pool-timeouts" && (report.Status != "healthy" || *report.Readiness.Pool.TimeoutsTotal != 9) {
					t.Fatal("cumulative counter became a current failure")
				}
			}
			for _, secret := range []string{"private-response-secret", "private-stream-secret", "owner-test-token"} {
				if strings.Contains(stdout.String()+stderr(), secret) {
					t.Fatal("health leaked untrusted content")
				}
			}
		})
	}
}

func TestDataAPIHealthRejectsUnsafeAndInconsistentResponses(t *testing.T) {
	valid := dataAPIHealthFixture(true, "disabled", 0)
	for _, body := range []string{
		strings.ReplaceAll(valid, `"engine":"reachable"`, `"engine":"private-secret"`),
		strings.ReplaceAll(valid, `"ready":true`, `"ready":false`),
		strings.ReplaceAll(valid, `"database":"reachable"`, `"database":"unknown"`),
		strings.ReplaceAll(valid, `"active":0`, `"active":3`),
		strings.ReplaceAll(valid, `"capacity":2`, `"capacity":1`),
		strings.ReplaceAll(valid, `"queued":0`, `"queued":9`),
		strings.ReplaceAll(valid, `"active":0,`, ``),
		valid + strings.Repeat(" ", api.DataAPIHealthMaxBytes),
		`private-upstream-error`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
		probe := probeDataAPIHealth(context.Background(), server.Client(), server.URL, false)
		server.Close()
		if probe.State != "invalid" || probe.Checks != nil || probe.Admission != nil {
			t.Fatalf("accepted unsafe response: %+v", probe)
		}
	}
}

func TestDataAPIHealthProbeDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if probe := probeDataAPIHealth(ctx, server.Client(), server.URL, false); probe.State != "unreachable" {
		t.Fatalf("deadline: %+v", probe)
	}
}
