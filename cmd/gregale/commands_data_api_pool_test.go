// adr: 650 — pool failures remain partial observations, preserving readiness.
package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDataAPIPoolValidationPreservesCoreHealthAndRedactsUnknownContent(t *testing.T) {
	base := dataAPIHealthPoolFixture(2)
	for _, scenario := range []struct {
		name, pool, state string
	}{
		{"observed", base, "observed"},
		{"coordinated", dataAPIHealthPoolFixture(1), "observed"},
		{"missing", "", "unsupported"},
		{"null", "null", "invalid"},
		{"untrusted-type", `"private-response-secret"`, "invalid"},
		{"unknown", `{"state":"unknown","checked_at":"2026-10-16T13:00:00+03:00","reason":"unsupported","private":"private-response-secret"}`, "unknown"},
		{"bad-state", strings.ReplaceAll(base, "observed", "private-response-secret"), "invalid"},
		{"bad-time", strings.ReplaceAll(base, "2026-10-16T10:00:00Z", "private-response-secret"), "invalid"},
		{"bad-capacity", strings.ReplaceAll(base, `"capacity":2`, `"capacity":3`), "invalid"},
		{"too-available", strings.ReplaceAll(base, `"available":0`, `"available":3`), "invalid"},
		{"negative", strings.ReplaceAll(base, `"waiting":0`, `"waiting":-1`), "invalid"},
		{"fractional", strings.ReplaceAll(base, `"waiting":0`, `"waiting":0.5`), "invalid"},
		{"unsafe-integer", strings.ReplaceAll(base, `"timeouts_total":0`, `"timeouts_total":9007199254740992`), "invalid"},
		{"missing-count", strings.ReplaceAll(base, `"waiting":0,`, ``), "invalid"},
		{"null-count", strings.ReplaceAll(base, `"waiting":0`, `"waiting":null`), "invalid"},
		{"unknown-with-count", `{"state":"unknown","checked_at":"2026-10-16T10:00:00Z","reason":"timeout","waiting":0}`, "invalid"},
		{"unknown-without-reason", `{"state":"unknown","checked_at":"2026-10-16T10:00:00Z"}`, "invalid"},
		{"untrusted-reason", `{"state":"unknown","checked_at":"2026-10-16T10:00:00Z","reason":"private-response-secret"}`, "invalid"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			refresh := "disabled"
			if scenario.name == "coordinated" {
				refresh = "active"
			}
			body := dataAPIHealthFixture(true, refresh, 0)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(body), &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, "pool")
			if scenario.pool != "" {
				fields["pool"] = json.RawMessage(scenario.pool)
			}
			content, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(content) }))
			defer server.Close()
			probe := main.ProbeDataAPIHealth(context.Background(), server.Client(), server.URL, false)
			if probe.State != "ok" || probe.Ready == nil || !*probe.Ready || probe.Pool == nil || probe.Pool.State != scenario.state {
				t.Fatalf("pool validation changed core readiness: %+v", probe)
			}
			encoded, err := json.Marshal(probe)
			if err != nil || strings.Contains(string(encoded), "private-response-secret") {
				t.Fatal("untrusted pool metadata leaked")
			}
			if scenario.name == "unknown" && probe.Pool.CheckedAt != "2026-10-16T10:00:00Z" {
				t.Fatal("timestamp was not normalized")
			}
		})
	}
}

func TestDataAPIPoolTextOutputSeparatesGaugesAndCumulativeCounter(t *testing.T) {
	for _, state := range []string{"observed", "unknown", "unsupported", "invalid"} {
		t.Run(state, func(t *testing.T) {
			stdout, _, restore := main.SwapIO(t)
			defer restore()
			pool := &main.DataAPIHealthPool{State: state}
			if state == "observed" {
				pool = main.ParseDataAPIHealthPool(json.RawMessage(dataAPIHealthPoolFixture(2)), 2)
			} else if state == "unknown" {
				pool.Reason, pool.CheckedAt = "timeout", "2026-10-16T10:00:00Z"
			}
			if err := main.WriteDataAPIPool(pool); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout.String(), "Pool: ") || (state == "observed" && !strings.Contains(stdout.String(), "cumulative acquisition timeouts 0")) {
				t.Fatalf("missing pool observation: %s", stdout.String())
			}
			if state != "observed" && strings.Contains(stdout.String(), "available 0") {
				t.Fatal("missing metrics were rendered as zero")
			}
		})
	}
}

func TestDataAPIPoolCounterResetIsNotAHealthIncident(t *testing.T) {
	for _, count := range []int{9, 0} {
		pool := main.ParseDataAPIHealthPool(json.RawMessage(strings.ReplaceAll(dataAPIHealthPoolFixture(2), `"timeouts_total":0`, fmt.Sprintf(`"timeouts_total":%d`, count))), 2)
		report := &main.DataAPIHealthReport{}
		main.DescribeDataAPIPool(report, pool)
		if report.Partial || len(report.CurrentFindings) != 0 {
			t.Fatal("a cumulative counter or reset became a current incident")
		}
	}
}
