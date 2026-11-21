package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261116190208: create sends the metric kind and guardrail options,
// results print guardrails and the recommendation, and ship takes a variant.
func TestFlagExperimentGuardrailAndShipCommands(t *testing.T) {
	var created api.FlagExperimentCreateRequest
	var shipped api.FlagExperimentShipRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		base := "/v1/projects/shop/environments/production/experiments"
		threshold := 180
		x := api.FlagExperiment{Key: "revenue", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed",
			MetricKind: "mean", Status: "running", LatencyThresholdMS: &threshold}
		switch r.Method + " " + r.URL.Path {
		case "POST " + base:
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(x)
		case "GET " + base + "/revenue/results":
			lift, low, high, p, d, dl, dh := 250.0, 40.0, 460.0, 0.001, 0.001, -0.002, 0.004
			_ = json.NewEncoder(w).Encode(api.FlagExperimentResults{Experiment: x,
				Control: api.FlagExperimentArm{Variant: "legacy", Exposed: 4000, Converted: 900, Rate: 2000},
				Variants: []api.FlagExperimentVariantResult{{FlagExperimentArm: api.FlagExperimentArm{Variant: "new", Exposed: 4000, Converted: 950, Rate: 2250},
					Lift: &lift, Low: &low, High: &high, PValue: &p, Status: "significant"}},
				Guardrails: []api.FlagExperimentGuardrail{{Name: "server_error_rate", Tolerance: 0.005, Status: "ok",
					Variants: []api.FlagExperimentGuardrailVariant{{Variant: "new", ControlRate: 0.01, VariantRate: 0.011, Difference: &d, Low: &dl, High: &dh, Status: "ok"}}}},
				Latency: []api.FlagExperimentLatency{{Variant: "legacy", Requests: 90000, P95MS: 180}, {Variant: "new", Requests: 91000, P95MS: 175}},
				Recommendation: api.FlagExperimentRecommendation{Action: "ship_variant", Variant: "new", Summary: "new is better than legacy",
					Commands: []string{"gregale flag experiment promote revenue new --project shop --environment production"}}})
		case "POST " + base + "/revenue/ship":
			_ = json.NewDecoder(r.Body).Decode(&shipped)
			x.Status, x.StopReason = "stopped", "shipped"
			_ = json.NewEncoder(w).Encode(api.FlagExperimentShipResponse{Experiment: x, ConfigVersion: 7})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	run := func(args ...string) (int, string, string) {
		main.ResetJSONOut(t)
		stdout, stderr, restore := main.SwapIO(t)
		code := main.Run(args)
		restore()
		return code, stdout.String(), stderr()
	}

	code, out, errOut := run("flag", "experiment", "create", "revenue", "--project", "shop", "--flag", "checkout", "--rule", "weighted",
		"--control", "legacy", "--metric", "order_completed", "--metric-kind", "mean", "--max-error-increase", "0.01")
	if code != 0 || !strings.Contains(out, "mean order_completed") {
		t.Fatalf("create = %d %q %q", code, out, errOut)
	}
	g := created.Guardrails
	if created.MetricKind != "mean" || g == nil || g.ServerErrorRate == nil || g.ServerErrorRate.MaxIncrease == nil || *g.ServerErrorRate.MaxIncrease != 0.01 ||
		g.ServerErrorRate.Disabled || g.P95Latency == nil || g.P95Latency.MaxIncrease != nil || g.P95Latency.Disabled {
		t.Fatalf("create request = %+v guardrails %+v", created, g)
	}
	if code, _, errOut := run("flag", "experiment", "create", "x", "--project", "shop", "--max-slow-increase", "lots"); code != 1 || !strings.Contains(errOut, "--max-slow-increase") {
		t.Fatalf("bad tolerance = %d %q", code, errOut)
	}

	code, out, _ = run("flag", "experiment", "result", "get", "revenue", "--project", "shop")
	for _, want := range []string{"MEAN", "2250", "+250", "server_error_rate", "+0.10 pts", "control's p95 of 180 ms", "p95 latency new: 175 ms",
		"Recommendation (ship_variant)", "gregale flag experiment promote revenue new"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("results = %d %q; want %q", code, out, want)
		}
	}

	code, out, _ = run("flag", "experiment", "promote", "revenue", "new", "--project", "shop")
	if code != 0 || shipped.Variant != "new" || !strings.Contains(out, "flags version 7") {
		t.Fatalf("ship = %d %q; request %+v", code, out, shipped)
	}
	if code, _, errOut := run("flag", "experiment", "promote", "revenue", "--project", "shop"); code != 1 || !strings.Contains(errOut, "VARIANT") {
		t.Fatalf("ship without a variant = %d %q", code, errOut)
	}
}
