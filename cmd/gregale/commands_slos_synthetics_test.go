package main_test

// adr: 747, 748 — `gregale slos` and `gregale synthetics` print the API's
// JSON unchanged under --json and require --app.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func sloSyntheticsTestServer(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/shop/slos":
			_ = json.NewEncoder(w).Encode([]api.SLOResponse{
				{ID: "slo-1", Name: "checkout-availability", SLI: "availability", ObjectivePct: 99.9, WindowDays: 30},
				{ID: "slo-2", Name: "checkout-latency", SLI: "latency", LatencyThresholdMS: 250, ObjectivePct: 99, WindowDays: 7},
			})
		case "GET /v1/apps/shop/synthetics":
			_ = json.NewEncoder(w).Encode([]api.SyntheticCheckResponse{
				{ID: "chk-1", Name: "health", Method: "GET", Path: "/healthz", TimeoutMS: 5000, IntervalSeconds: 300, Enabled: true},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
}

func TestCmdSLOsAndSyntheticsJSONOutput(t *testing.T) {
	main.ResetJSONOutput()
	sloSyntheticsTestServer(t)
	buf := captureChannelsStdout(t)
	(*main.JsonOutput) = true
	defer func() { main.ResetJSONOutput() }()

	if code := main.CmdSLOs([]string{"list", "--app", "shop"}); code != 0 {
		t.Fatalf("gregale slos list --json = %d", code)
	}
	var slos []api.SLOResponse
	if err := json.Unmarshal(buf.Bytes(), &slos); err != nil || len(slos) != 2 || slos[1].LatencyThresholdMS != 250 {
		t.Fatalf("slos list --json = %v %s", err, buf.String())
	}

	buf.Reset()
	if code := main.CmdSynthetics([]string{"list", "--app", "shop"}); code != 0 {
		t.Fatalf("gregale synthetics list --json = %d", code)
	}
	var checks []api.SyntheticCheckResponse
	if err := json.Unmarshal(buf.Bytes(), &checks); err != nil || len(checks) != 1 || checks[0].IntervalSeconds != 300 {
		t.Fatalf("synthetics list --json = %v %s", err, buf.String())
	}
}

func TestCmdSLOsAndSyntheticsRequireApp(t *testing.T) {
	main.ResetJSONOutput()
	sloSyntheticsTestServer(t)
	captureChannelsStdout(t)
	tests := []struct {
		name string
		run  func([]string) int
		args []string
	}{
		{"slos without subcommand", main.CmdSLOs, nil},
		{"slos list without app", main.CmdSLOs, []string{"list"}},
		{"synthetics without subcommand", main.CmdSynthetics, nil},
		{"synthetics list without app", main.CmdSynthetics, []string{"list"}},
	}
	for _, tt := range tests {
		if code := tt.run(tt.args); code == 0 {
			t.Errorf("%s: exit 0, want an error", tt.name)
		}
	}
}
