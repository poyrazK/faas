package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261115163113 §6: `gregale flag experiment ...` drives the
// experiments API; `flag experiment result get` prints each variant's lift.
func TestFlagExperimentCommands(t *testing.T) {
	var created api.FlagExperimentCreateRequest
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		base := "/v1/projects/shop/environments/production/experiments"
		x := api.FlagExperiment{Key: "checkout-v2", Flag: "checkout", RuleID: "weighted", Control: "legacy", MetricEvent: "order_completed", Status: "running"}
		switch r.Method + " " + r.URL.Path {
		case "POST " + base:
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(x)
		case "GET " + base:
			_ = json.NewEncoder(w).Encode(api.FlagExperimentList{Experiments: []api.FlagExperiment{x}})
		case "GET " + base + "/checkout-v2/results":
			lift, low, high, p := 0.03, 0.012, 0.048, 0.0004
			_ = json.NewEncoder(w).Encode(api.FlagExperimentResults{Experiment: x,
				Control: api.FlagExperimentArm{Variant: "legacy", Exposed: 5000, Converted: 500, Rate: 0.1},
				Variants: []api.FlagExperimentVariantResult{{FlagExperimentArm: api.FlagExperimentArm{Variant: "new", Exposed: 5000, Converted: 650, Rate: 0.13},
					Lift: &lift, Low: &low, High: &high, PValue: &p, Status: "significant"}}})
		case "POST " + base + "/checkout-v2/stop":
			x.Status = "stopped"
			_ = json.NewEncoder(w).Encode(x)
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

	code, out, errOut := run("flag", "experiment", "create", "checkout-v2", "--project", "shop", "--flag", "checkout", "--rule", "weighted",
		"--control", "legacy", "--metric", "order_completed", "--mixing-lift", "0.05", "--start")
	if code != 0 || !strings.Contains(out, "checkout-v2") {
		t.Fatalf("create = %d %q %q", code, out, errOut)
	}
	if created.Flag != "checkout" || created.RuleID != "weighted" || created.Control != "legacy" || created.MetricEvent != "order_completed" ||
		!created.Start || created.MixingLift == nil || *created.MixingLift != 0.05 {
		t.Fatalf("create request = %+v", created)
	}
	if code, out, _ := run("flag", "experiment", "list", "--project", "shop"); code != 0 || !strings.Contains(out, "order_completed") {
		t.Fatalf("list = %d %q", code, out)
	}
	code, out, _ = run("flag", "experiment", "result", "get", "checkout-v2", "--project", "shop")
	for _, want := range []string{"legacy (control)", "+3.00 pts", "[+1.20, +4.80]", "significant"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("results = %d %q; want %q", code, out, want)
		}
	}
	if code, out, _ := run("flags", "experiments", "stop", "checkout-v2", "--project", "shop"); code != 0 || !strings.Contains(out, "stopped") {
		t.Fatalf("stop (legacy spelling) = %d %q", code, out)
	}
	if code, _, errOut := run("flag", "experiment", "get", "--project", "shop"); code != 1 || !strings.Contains(errOut, "experiment KEY") {
		t.Fatalf("get without a key = %d %q", code, errOut)
	}
	if want := fmt.Sprint([]string{
		"POST /v1/projects/shop/environments/production/experiments",
		"GET /v1/projects/shop/environments/production/experiments",
		"GET /v1/projects/shop/environments/production/experiments/checkout-v2/results",
		"POST /v1/projects/shop/environments/production/experiments/checkout-v2/stop",
	}); fmt.Sprint(seen) != want {
		t.Fatalf("requests = %v; want %s", seen, want)
	}
}
