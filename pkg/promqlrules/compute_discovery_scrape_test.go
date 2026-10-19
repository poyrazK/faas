//go:build integration

package promqlrules_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type discoveryScrapeState struct {
	targets, registry, invalid         int
	refreshAge                         time.Duration
	bootstrap, absent, down, duplicate bool
}

// TestComputeMetricsDiscoveryScrapeLabels exercises actual scrape label
// collision handling, HTTP-SD, recording joins, and alert evaluation together.
// promtool fixtures separately exercise the unchanged production hold times.
func TestComputeMetricsDiscoveryScrapeLabels(t *testing.T) {
	binary, err := exec.LookPath("prometheus")
	if err != nil {
		t.Skip("prometheus not installed; CI installs the pinned binary for this test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	var mu sync.RWMutex
	current := discoveryScrapeState{targets: 1, registry: 1}
	getState := func() discoveryScrapeState { mu.RLock(); defer mu.RUnlock(); return current }
	compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if getState().down {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "vmmd_daemon_ready{daemon=\"vmmd\"} 0\n")
	}))
	defer compute.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := getState()
		if r.URL.Path == "/targets" {
			groups := []map[string]any{}
			if !s.absent {
				groups = append(groups, map[string]any{"targets": []string{compute.Listener.Addr().String()}, "labels": map[string]string{"node": "compute-1"}})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(groups)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		writeDiscoveryExposition(w, s, "vmmd")
		// apid preinitializes unused jobs. Their unrequested zero refreshes
		// must neither raise stale warnings nor satisfy vmmd's joins.
		writeDiscoveryExposition(w, discoveryScrapeState{bootstrap: true}, "gatewayd-internal")
	}))
	defer source.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		s := getState()
		if s.duplicate {
			writeDiscoveryExposition(w, s, "vmmd")
		}
	}))
	defer second.Close()
	base := startDiscoveryPrometheus(t, ctx, binary, source, second)
	client := &http.Client{Timeout: time.Second}
	awaitDiscoverySnapshot(t, ctx, client, base, discoveryScrapeState{targets: 1, registry: 1}, inputCoverage(1), nil)
	correct, err := discoveryVector(ctx, client, base, `apid_metrics_discovery_targets{job="apid",exported_job="vmmd"}`)
	if err != nil || len(correct) != 1 || correct[0].Metric["instance"] != source.Listener.Addr().String() {
		t.Fatalf("default scrape collision mapping: %+v %v", correct, err)
	}
	wrong, err := discoveryVector(ctx, client, base, `apid_metrics_discovery_targets{job="vmmd"}`)
	if err != nil || len(wrong) != 0 {
		t.Fatalf("producer job was not exported_job: %+v %v", wrong, err)
	}
	for _, tc := range []struct {
		name     string
		state    discoveryScrapeState
		coverage *float64
		alerts   []string
	}{
		{"missing target", discoveryScrapeState{targets: 2, registry: 2}, inputCoverage(.5), []string{"FaasComputeMetricsScrapeCoverageLow"}},
		{"all targets absent", discoveryScrapeState{targets: 2, registry: 2, absent: true}, inputCoverage(0), []string{"FaasComputeMetricsScrapeCoverageLow"}},
		{"failed scrape", discoveryScrapeState{targets: 1, registry: 1, down: true}, inputCoverage(0), []string{"FaasComputeMetricsScrapeCoverageLow", "FaasComputeMetricsScrapeTargetDown"}},
		{"stale refresh", discoveryScrapeState{targets: 1, registry: 1, down: true, refreshAge: 3 * time.Minute}, inputCoverage(0), []string{"FaasComputeMetricsDiscoveryStale"}},
		{"bootstrap zero", discoveryScrapeState{targets: 1, registry: 1, down: true, bootstrap: true}, inputCoverage(0), []string{"FaasComputeMetricsDiscoveryStale"}},
		{"future refresh", discoveryScrapeState{targets: 1, registry: 1, down: true, refreshAge: -time.Hour}, inputCoverage(0), []string{"FaasComputeMetricsDiscoveryStale"}},
		{"partly invalid registry", discoveryScrapeState{targets: 1, registry: 2, invalid: 1}, inputCoverage(1), []string{"FaasComputeMetricsDiscoveryInvalidTargets"}},
		{"entirely invalid registry", discoveryScrapeState{registry: 2, invalid: 2, absent: true}, inputCoverage(0), []string{"FaasComputeMetricsDiscoveryEmpty", "FaasComputeMetricsDiscoveryInvalidTargets"}},
		{"idle registry", discoveryScrapeState{absent: true}, inputCoverage(0), nil},
		{"ambiguous producer", discoveryScrapeState{targets: 1, registry: 1, duplicate: true}, nil, []string{"FaasComputeMetricsDiscoveryAmbiguous"}},
		{"recovered", discoveryScrapeState{targets: 1, registry: 1}, inputCoverage(1), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			current = tc.state
			mu.Unlock()
			awaitDiscoverySnapshot(t, ctx, client, base, tc.state, tc.coverage, tc.alerts)
		})
	}
}

func writeDiscoveryExposition(w io.Writer, s discoveryScrapeState, job string) {
	requests, outcome := 1, "success"
	if s.bootstrap {
		outcome = "unavailable"
	}
	if job == "gatewayd-internal" {
		requests = 0
	}
	_, _ = fmt.Fprintf(w, "apid_metrics_discovery_requests_total{job=%q,outcome=%q} %d\n", job, outcome, requests)
	success := time.Now().Add(-s.refreshAge).Unix()
	if s.bootstrap {
		success = 0
	}
	for _, metric := range []struct {
		name  string
		value int64
	}{
		{"targets", int64(s.targets)}, {"registry_nodes", int64(s.registry)}, {"invalid_targets", int64(s.invalid)}, {"last_success_timestamp_seconds", success},
	} {
		_, _ = fmt.Fprintf(w, "apid_metrics_discovery_%s{job=%q} %d\n", metric.name, job, metric.value)
	}
}

func startDiscoveryPrometheus(t *testing.T, ctx context.Context, binary string, source, second *httptest.Server) string {
	t.Helper()
	var doc struct {
		Groups []struct {
			Name     string           `yaml:"name"`
			Interval string           `yaml:"interval"`
			Rules    []map[string]any `yaml:"rules"`
		} `yaml:"groups"`
	}
	raw, err := os.ReadFile(filepath.Join(repoRootDir, "deploy/ansible/roles/prometheus/files/faas.rules.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, group := range doc.Groups {
		if group.Name != "faas_compute_metrics_discovery" {
			continue
		}
		found = true
		// Only the temporary copy accelerates evaluation and alert activation.
		// Checked-in promtool fixtures assert the real 5m and 10m holds.
		group.Interval = "100ms"
		for _, rule := range group.Rules {
			if _, ok := rule["alert"]; ok {
				rule["for"] = "0s"
			}
		}
		doc.Groups = doc.Groups[:0]
		doc.Groups = append(doc.Groups, group)
		break
	}
	if !found {
		t.Fatal("compute discovery rule group missing")
	}
	dir := t.TempDir()
	rules, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	rulePath := filepath.Join(dir, "rules.yml")
	if err := os.WriteFile(rulePath, rules, 0o600); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"global":     map[string]string{"scrape_interval": "100ms", "scrape_timeout": "100ms", "evaluation_interval": "100ms"},
		"rule_files": []string{rulePath},
		"scrape_configs": []any{
			map[string]any{"job_name": "apid", "static_configs": []any{map[string]any{"targets": []string{source.Listener.Addr().String(), second.Listener.Addr().String()}}}},
			map[string]any{"job_name": "vmmd", "http_sd_configs": []any{map[string]string{"url": source.URL + "/targets", "refresh_interval": "100ms"}}},
		},
	}
	configData, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "prometheus.yml")
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "prometheus.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.CommandContext(ctx, binary, "--config.file="+configPath, "--storage.tsdb.path="+filepath.Join(dir, "data"), "--web.listen-address="+address, "--log.level=warn")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			data, _ := os.ReadFile(logPath)
			t.Logf("Prometheus log: %s", data)
		}
	})
	return "http://" + address
}

type discoverySample struct {
	Metric map[string]string  `json:"metric"`
	Value  [2]json.RawMessage `json:"value"`
}

func discoveryVector(ctx context.Context, client *http.Client, base, query string) ([]discoverySample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/query?"+url.Values{"query": {query}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string            `json:"resultType"`
			Result     []discoverySample `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK || out.Status != "success" || out.Data.ResultType != "vector" {
		return nil, fmt.Errorf("invalid query result: HTTP %d status %s", resp.StatusCode, out.Status)
	}
	return out.Data.Result, nil
}

func inputCoverage(value float64) *float64 { return &value }

func discoveryValue(rows []discoverySample, expected float64) bool {
	value, valid := discoveryNumber(rows)
	return valid && value == expected
}

func discoveryNumber(rows []discoverySample) (float64, bool) {
	if len(rows) != 1 || len(rows[0].Metric) != 2 || rows[0].Metric["job"] != "vmmd" {
		return 0, false
	}
	var text string
	if json.Unmarshal(rows[0].Value[1], &text) != nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil
}

func awaitDiscoverySnapshot(t *testing.T, ctx context.Context, client *http.Client, base string, state discoveryScrapeState, coverage *float64, alerts []string) {
	t.Helper()
	expected := append([]string{}, alerts...)
	slices.Sort(expected)
	// Prometheus 2.54 batches discovery updates on a five-second cadence.
	// Leave room for one update, target removal markers, and evaluation.
	deadline := time.Now().Add(12 * time.Second)
	var last any
	for time.Now().Before(deadline) && ctx.Err() == nil {
		targets, terr := discoveryVector(ctx, client, base, `faas_compute_metrics_discovery_targets:by_job{job="vmmd"}`)
		registry, rerr := discoveryVector(ctx, client, base, `faas_compute_metrics_discovery_registry_nodes:by_job{job="vmmd"}`)
		invalid, ierr := discoveryVector(ctx, client, base, `faas_compute_metrics_discovery_invalid_targets:by_job{job="vmmd"}`)
		success, serr := discoveryVector(ctx, client, base, `faas_compute_metrics_discovery_last_success_timestamp_seconds:by_job{job="vmmd"}`)
		cov, cerr := discoveryVector(ctx, client, base, `faas_compute_metrics_scrape_coverage:by_job{job="vmmd"}`)
		active, aerr := discoveryVector(ctx, client, base, `ALERTS{alertstate="firing"}`)
		names := []string{}
		for _, row := range active {
			names = append(names, row.Metric["alertname"])
			if row.Metric["job"] != "vmmd" {
				names = append(names, "wrong_job")
			}
		}
		slices.Sort(names)
		valid := terr == nil && rerr == nil && ierr == nil && serr == nil && cerr == nil && aerr == nil && reflect.DeepEqual(expected, names)
		if state.duplicate {
			valid = valid && len(targets) == 0 && len(registry) == 0 && len(invalid) == 0 && len(success) == 0 && len(cov) == 0
		} else {
			valid = valid && discoveryValue(targets, float64(state.targets)) && discoveryValue(registry, float64(state.registry)) && discoveryValue(invalid, float64(state.invalid)) && coverage != nil && discoveryValue(cov, *coverage)
			stamp, ok := discoveryNumber(success)
			if state.bootstrap {
				valid = valid && ok && stamp == 0
			} else {
				want := float64(time.Now().Add(-state.refreshAge).Unix())
				valid = valid && ok && stamp >= want-2 && stamp <= want+2
			}
		}
		if valid {
			return
		}
		last = map[string]any{"targets": targets, "registry": registry, "invalid": invalid, "success": success, "coverage": cov, "alerts": names, "errors": []error{terr, rerr, ierr, serr, cerr, aerr}}
		select {
		case <-ctx.Done():
		case <-time.After(25 * time.Millisecond):
		}
	}
	t.Fatalf("expected state %+v alerts %v coverage %v; last=%+v", state, expected, coverage, last)
}
