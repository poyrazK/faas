package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestObsRuntimeDiagnosticsStackSmoke(t *testing.T) {
	auth, enabled := os.LookupEnv("OBS_STACK_GRAFANA_AUTH")
	if !enabled {
		t.Skip("requires the provisioned stack from scripts/ci/obs_stack_smoke.sh")
	}
	user, password, ok := strings.Cut(auth, ":")
	if !ok || user == "" || password == "" {
		t.Fatal("OBS_STACK_GRAFANA_AUTH must contain Grafana credentials")
	}
	grafana := runtimeSmokeGrafana{user: user, password: password, client: &http.Client{Timeout: 5 * time.Second}}
	const grafanaAPI = "http://127.0.0.1:3000/api"
	panels := grafana.runtimePanels(t, grafanaAPI)
	queryClient := promql.NewClient(grafanaAPI+"/datasources/proxy/uid/prometheus", grafana)

	// Match the Prometheus role's default apid scrape target on the smoke host.
	const instance = "127.0.0.1:9101"
	e := gapEnv(t)
	server := *e.S()
	listener, err := net.Listen("tcp4", instance)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor((*server.Ops()).Registry(), promhttp.HandlerOpts{}))
	metrics := &httptest.Server{Listener: listener, Config: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}}
	metrics.Start()
	t.Cleanup(metrics.Close)

	const reports = `apid_operator_runtime_reports_total{job="apid",instance="` + instance + `"}`
	const deadlines = `apid_operator_runtime_collection_deadlines_total{job="apid",instance="` + instance + `",phase="core_evidence",origin="collection_budget"}`
	const collectionDeadline = instance + "/core_evidence/collection_budget"
	reportKey := func(labels map[string]string) string { return labels["outcome"] }
	deadlineKey := func(labels map[string]string) string {
		return labels["instance"] + "/" + labels["phase"] + "/" + labels["origin"]
	}

	// A counter needs a pre-event scrape for the provisioned increase queries.
	waitRuntimeSmokeQuery(t, queryClient, reports, reportKey, map[string]float64{"complete": 0, "deadline_exceeded": 0}, false)
	waitRuntimeSmokeQuery(t, queryClient, deadlines, deadlineKey, map[string]float64{collectionDeadline: 0}, false)
	waitRuntimeSmokeQuery(t, queryClient, panels[18], reportKey, map[string]float64{"not_collected": -1}, false)

	now := time.Now().UTC()
	release := strings.Repeat("a", 40)
	source := httptest.NewServer(runtimeSmokeSource(t, now, release))
	t.Cleanup(source.Close)
	blocked := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(blocked.Close)
	path := operatorobs.RuntimeBuildsPath + "?release=" + release + "&after=" + now.Add(-time.Minute).Format(time.RFC3339) + "&expected_role=single-box-v2&diagnostics=none"
	for _, collection := range []struct{ baseURL, outcome string }{
		{source.URL, "complete"},
		{blocked.URL, "deadline_exceeded"},
	} {
		server.WithStatusCache(collection.baseURL, "")
		response := e.Do(t, http.MethodGet, path, nil, nil)
		var report operatorobs.RuntimeBuildsLoopResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &report) != nil || !operatorobs.RuntimeBuildsValid(report.ObsRuntimeBuilds) {
			t.Fatalf("protected runtime report returned HTTP %d with an invalid response: %s", response.Code, response.Body)
		}
		waitRuntimeSmokeQuery(t, queryClient, panels[18], reportKey, map[string]float64{collection.outcome: 1}, true)
	}

	wantReports := map[string]float64{"complete": 1, "deadline_exceeded": 1}
	wantDeadline := map[string]float64{collectionDeadline: 1}
	waitRuntimeSmokeQuery(t, queryClient, reports, reportKey, wantReports, false)
	waitRuntimeSmokeQuery(t, queryClient, deadlines, deadlineKey, wantDeadline, false)
	// increase extrapolates between scrapes. Check exact counts above and
	// positive series with the expected labels in the dashboard's expressions.
	waitRuntimeSmokeQuery(t, queryClient, panels[3], reportKey, wantReports, true)
	waitRuntimeSmokeQuery(t, queryClient, panels[11], deadlineKey, wantDeadline, true)
}

type runtimeSmokeGrafana struct {
	user, password string
	client         *http.Client
}

func (g runtimeSmokeGrafana) Do(r *http.Request) (*http.Response, error) {
	r.SetBasicAuth(g.user, g.password)
	return g.client.Do(r)
}

func (g runtimeSmokeGrafana) runtimePanels(t *testing.T, api string) map[int]string {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, api+"/dashboards/uid/faas-runtime-diagnostics", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Grafana runtime diagnostics dashboard returned HTTP %d", res.StatusCode)
	}
	var provisioned struct {
		Meta      struct{ Provisioned bool }
		Dashboard struct {
			Templating struct {
				List []struct {
					Name       string
					AllValue   string
					IncludeAll bool
					Current    struct{ Value any }
				}
			}
			Panels []struct {
				ID         int
				Datasource struct{ UID string }
				Targets    []struct{ Expr string }
			}
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&provisioned); err != nil {
		t.Fatal(err)
	}
	if !provisioned.Meta.Provisioned {
		t.Fatal("Grafana runtime diagnostics dashboard is not provisioned")
	}
	var allInstances string
	for _, variable := range provisioned.Dashboard.Templating.List {
		if variable.Name == "apid_instance" && variable.IncludeAll && variable.Current.Value == "$__all" {
			allInstances = variable.AllValue
			break
		}
	}
	if allInstances == "" {
		t.Fatal("runtime diagnostics dashboard lacks the default All instances selection")
	}
	queries := map[int]string{}
	for _, panel := range provisioned.Dashboard.Panels {
		if panel.ID != 3 && panel.ID != 11 && panel.ID != 18 {
			continue
		}
		if panel.Datasource.UID != "prometheus" || len(panel.Targets) != 1 || panel.Targets[0].Expr == "" {
			t.Fatalf("runtime panel %d lacks its Prometheus expression", panel.ID)
		}
		// The datasource proxy takes raw PromQL. Grafana substitutes the
		// custom All value in the browser when it runs the panel.
		queries[panel.ID] = strings.ReplaceAll(panel.Targets[0].Expr, "$apid_instance", allInstances)
	}
	if len(queries) != 3 {
		t.Fatal("runtime diagnostics dashboard lacks the report, collection deadline, or last collection panel")
	}
	return queries
}

func waitRuntimeSmokeQuery(t *testing.T, client *promql.Client, query string, key func(map[string]string) string, want map[string]float64, positive bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		samples, err := client.QueryVectorStrict(ctx, query)
		got := map[string]float64{}
		for _, s := range samples {
			k := key(s.Labels)
			if positive && s.Value > 0 {
				got[k]++
			} else if _, expected := want[k]; !positive && (expected || s.Value != 0) {
				got[k] = s.Value
			}
		}
		if err == nil && maps.Equal(want, got) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Grafana Prometheus query %q got %v, want %v (last error: %v)", query, got, want, err)
		case <-ticker.C:
		}
	}
}

// Serve healthy identity, readiness, process and loop samples for the
// protected single-box-v2 read with optional diagnostic groups disabled.
func runtimeSmokeSource(t *testing.T, now time.Time, release string) http.Handler {
	type row struct {
		Metric map[string]string `json:"metric"`
		Value  [2]any            `json:"value"`
	}
	queries := map[string][]row{}
	add := func(query string, labels map[string]string, value string) {
		queries[query] = append(queries[query], row{labels, [2]any{now.Unix(), value}})
		stamp := "timestamp(" + query + ")"
		queries[stamp] = append(queries[stamp], row{labels, [2]any{now.Unix(), fmt.Sprint(now.Unix())}})
	}
	for _, spec := range []struct{ job, daemon, prefix, extraLoop string }{
		{"apid", "apid", "apid", ""},
		{"gatewayd", "gatewayd-public", "gatewayd_public", ""},
		{"schedd", "schedd", "schedd", "main"},
		{"vmmd", "vmmd", "vmmd", "sweep"},
		{"builderd", "builderd", "builderd", ""},
		{"imaged", "imaged", "imaged", ""},
	} {
		target := map[string]string{"job": spec.job, "instance": spec.job + ":9100"}
		add(fmt.Sprintf(`up{job=%q}`, spec.job), target, "1")
		daemon := maps.Clone(target)
		daemon["daemon"] = spec.daemon
		selector := fmt.Sprintf(`{job=%q,daemon=%q}`, spec.job, spec.daemon)
		build := maps.Clone(daemon)
		build["git_sha"], build["version"], build["build_time"] = release, "v1.2.3", now.Format(time.RFC3339)
		add(spec.prefix+"_daemon_build_info"+selector, build, "1")
		add(spec.prefix+"_daemon_ready"+selector, daemon, "1")
		add(spec.prefix+"_daemon_uptime_seconds"+selector, daemon, "130")
		for _, reason := range []string{"ready", "draining", "database", "vmmd", "grpc", "storage", "credentials", "stale", "process", "other"} {
			labels := maps.Clone(daemon)
			labels["reason"] = reason
			value := "0"
			if reason == "ready" {
				value = "1"
			}
			add(spec.prefix+"_daemon_ready_reason"+selector, labels, value)
		}
		for _, loop := range []string{"runtime", spec.extraLoop} {
			if loop == "" {
				continue
			}
			labels := maps.Clone(target)
			labels["loop"] = loop
			selector := fmt.Sprintf(`{job=%q,loop=%q}`, spec.job, loop)
			add(spec.prefix+"_loop_stalled"+selector, labels, "0")
			add(spec.prefix+"_loop_last_beat_age_seconds"+selector, labels, "1")
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		rows, ok := queries[query]
		if !ok || r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected runtime source read: %s %q", r.URL.Path, query)
			http.Error(w, "unexpected runtime source read", http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"status": "success", "data": map[string]any{"resultType": "vector", "result": rows},
		}); err != nil {
			t.Errorf("write runtime source samples: %v", err)
		}
	})
}
