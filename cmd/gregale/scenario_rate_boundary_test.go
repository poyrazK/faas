package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestLocalLoadSuccessRateBoundary(t *testing.T) {
	stdout, _ := main.CaptureSuiteTestOutput(t)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if count.Add(1) <= 4 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "gregale-test.yaml")
	main.WriteSuiteTestFile(t, manifest, `version: 1
scenarios:
  audit:
    project: audit-app
    requests:
      - {name: health, method: GET, path: /, expect: {status: 200}}
    load:
      vus: 1
      iterations: 5
      thresholds: {success_rate: 0.2}
`)
	code, problem := main.RunWithStderr(t, func() int {
		return main.CmdTest([]string{"--manifest", manifest, "--scenario", "audit", "--engine", "local", "--base-url", server.URL, "--load"})
	})
	var receipts []main.TestRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || code != 0 || len(receipts) != 1 || receipts[0].Load == nil || receipts[0].Load.Failures != 4 || receipts[0].Load.Requests != 5 || receipts[0].Status != "passed" {
		t.Fatalf("load exit %d: %s; %s (%v)", code, problem, stdout, err)
	}
}

func TestLoadRateThresholdBoundaries(t *testing.T) {
	threshold := 0.2
	for _, tc := range []struct {
		name               string
		requests, failures int
		want               bool
	}{
		{"exact boundary", 5, 4, true}, {"below boundary", 6, 5, false}, {"no samples", 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var success float64
			if tc.requests > 0 {
				success = 1 - float64(tc.failures)/float64(tc.requests)
			}
			metrics := main.TestLoadMetrics{Requests: tc.requests, Failures: tc.failures, SuccessRate: success}
			checks := main.TestLoadThresholdEvidenceFor(metrics, 0, 0, nil, &threshold, 0)
			if len(checks) != 1 || checks[0].Passed != tc.want {
				t.Fatalf("checks=%+v", checks)
			}
		})
	}
}

func TestRegressionRateBoundaries(t *testing.T) {
	base, current := &main.TestLoadEvidence{}, &main.TestLoadEvidence{}
	base.Requests, base.Failures, base.ErrorRate, base.SuccessRate = 20, 7, 0.35, 0.65
	current.Requests, current.Failures, current.ErrorRate, current.SuccessRate = 20, 8, 0.4, 0.6
	base.Latency.Samples, current.Latency.Samples = 20, 20
	base.Latency.P95MS, current.Latency.P95MS = 100, 100
	cfg := main.ResolveTestRegressionConfig(nil)
	cfg.ErrorRateIncrease = 0.05
	relative := main.TestLoadRelativeSpec{MinSamples: 20, ErrorRateIncrease: &cfg.ErrorRateIncrease}
	for _, tc := range []struct {
		name     string
		failures int
		want     bool
	}{{"boundary", 8, true}, {"exceeds", 9, false}} {
		t.Run(tc.name, func(t *testing.T) {
			current.Failures = tc.failures
			current.ErrorRate = float64(tc.failures) / 20
			baseline := main.CompareTestBaseline(base, current, cfg, "digest")
			staged := main.CompareTestStagedLoadRelative("baseline", base, current, &relative)
			if (baseline.Status == "passed") != tc.want || (staged.Status == "passed") != tc.want {
				t.Fatalf("baseline=%+v staged=%+v", baseline, staged)
			}
		})
	}
}

func TestReportErrorRateBudgetBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after float64
		want  string
	}{{"boundary", 11.0 / 20, "passed"}, {"exceeds", 12.0 / 20, "failed"}} {
		t.Run(tc.name, func(t *testing.T) {
			check := main.CompareTestGateErrorRate("run", "load.error_rate", 9.0/20, tc.after, 10)
			repeated := main.CompareRepeatedTestGateErrorRate("run", "load.error_rate", []float64{9.0 / 20, 9.0 / 20}, []float64{tc.after, tc.after}, 10, 1, 2)
			if check.Status != tc.want || repeated.Status != tc.want {
				t.Fatalf("single=%+v repeated=%+v", check, repeated)
			}
		})
	}
	spread := main.CompareRepeatedTestGateErrorRate("run", "load.error_rate", []float64{0.45, 0.55}, []float64{0.45, 0.55}, 0, 10, 2)
	if spread.Status != "passed" {
		t.Fatalf("inclusive repeated spread: %+v", spread)
	}
}
