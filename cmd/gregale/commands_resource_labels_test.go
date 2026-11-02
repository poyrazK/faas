package main_test

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const labelledDatabaseID = "7f5f3c1e-6a0d-4a3b-9c55-0d2a1b7e9f10"

// resourceLabelsServer serves labelled databases, crons and jobs. Like an
// apid without labels it ignores ?selector=, so the tests see the CLI's own
// match. It records every label write by path.
type resourceLabelsServer struct {
	mu     sync.Mutex
	labels map[string]map[string]string // label path → labels
	writes map[string][]api.LabelChange
}

func newResourceLabelsServer(t *testing.T) *resourceLabelsServer {
	t.Helper()
	rs := &resourceLabelsServer{
		labels: map[string]map[string]string{
			"/v1/postgres/databases/" + labelledDatabaseID: {"team": "billing"},
			"/v1/crons/cron-billing":                       {"team": "billing"},
			"/v1/crons/cron-search":                        {"team": "search"},
			"/v1/jobs/nightly-report":                      {"team": "billing", "tier": "batch"},
			"/v1/jobs/search-index":                        {"team": "search"},
		},
		writes: map[string][]api.LabelChange{},
	}
	srv := httptest.NewServer(http.HandlerFunc(rs.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_labels")
	return rs
}

func (rs *resourceLabelsServer) serve(w http.ResponseWriter, r *http.Request) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	database := func() api.ManagedPostgresDatabase {
		return api.ManagedPostgresDatabase{ID: labelledDatabaseID, Name: "orders", State: "ready", Labels: rs.labels["/v1/postgres/databases/"+labelledDatabaseID]}
	}
	cron := func(id string) api.CronResponse {
		return api.CronResponse{ID: id, Schedule: "* * * * *", Path: "/" + id, Enabled: true, Labels: rs.labels["/v1/crons/"+id]}
	}
	job := func(name string) api.JobResponse {
		return api.JobResponse{Name: name, Kind: "batch", Labels: rs.labels["/v1/jobs/"+name]}
	}
	switch {
	case r.Method == http.MethodGet && path == "/v1/postgres/databases":
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{database(), {ID: "8a1b", Name: "events", State: "ready"}}})
		return
	case r.Method == http.MethodGet && path == "/v1/postgres/databases/"+labelledDatabaseID:
		_ = json.NewEncoder(w).Encode(database())
		return
	case r.Method == http.MethodGet && path == "/v1/crons":
		_ = json.NewEncoder(w).Encode([]api.CronResponse{cron("cron-billing"), cron("cron-search")})
		return
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/crons/"):
		_ = json.NewEncoder(w).Encode(cron(strings.TrimPrefix(path, "/v1/crons/")))
		return
	case r.Method == http.MethodGet && path == "/v1/jobs":
		jobs := []api.JobResponse{job("nightly-report"), job("search-index")}
		_ = json.NewEncoder(w).Encode(api.ListJobsResponse{Jobs: jobs, Total: len(jobs), NextOffset: -1})
		return
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/jobs/"):
		_ = json.NewEncoder(w).Encode(job(strings.TrimPrefix(path, "/v1/jobs/")))
		return
	case r.Method == http.MethodPatch && strings.HasSuffix(path, "/labels"):
		resource := strings.TrimSuffix(path, "/labels")
		if _, ok := rs.labels[resource]; !ok {
			break
		}
		var change api.LabelChange
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &change)
		rs.writes[resource] = append(rs.writes[resource], change)
		next := maps.Clone(rs.labels[resource])
		for _, k := range change.Remove {
			delete(next, k)
		}
		maps.Copy(next, change.Set)
		rs.labels[resource] = next
		_ = json.NewEncoder(w).Encode(api.LabelsResponse{Labels: next})
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found"}`))
}

// postgres|cron|job label set/unset/list send one write per command to the
// resource's label route and print the labels it carries after it.
func TestResourceLabelCommands(t *testing.T) {
	rs := newResourceLabelsServer(t)
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"postgres", "label", "set", "orders", "tier=primary"}, []string{"team", "billing", "tier", "primary"}},
		{[]string{"postgres", "label", "list", "orders"}, []string{"KEY", "tier", "primary"}},
		{[]string{"cron", "label", "set", "cron-search", "owner=ops"}, []string{"owner", "ops", "team", "search"}},
		{[]string{"cron", "label", "unset", "cron-search", "team"}, []string{"owner"}},
		{[]string{"job", "label", "unset", "nightly-report", "tier"}, []string{"team", "billing"}},
		{[]string{"job", "label", "list", "search-index"}, []string{"team", "search"}},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr := captureOutput(t)
			if code := main.Run(tc.args); code != 0 {
				t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("output missing %q:\n%s", want, stdout)
				}
			}
		})
	}
	if w := rs.writes["/v1/postgres/databases/"+labelledDatabaseID]; len(w) != 1 || w[0].Set["tier"] != "primary" {
		t.Errorf("database writes = %+v", w)
	}
	if w := rs.writes["/v1/crons/cron-search"]; len(w) != 2 || w[1].Remove[0] != "team" {
		t.Errorf("cron writes = %+v", w)
	}
	if w := rs.writes["/v1/jobs/nightly-report"]; len(w) != 1 || w[0].Remove[0] != "tier" {
		t.Errorf("job writes = %+v", w)
	}
}

// A missing resource is a 404 that keeps exit 4 and says labels may not be
// available yet; invalid labels are refused before any request.
func TestResourceLabelCommandsRefuse(t *testing.T) {
	rs := newResourceLabelsServer(t)
	tests := []struct {
		args      []string
		code      int
		wantError string
	}{
		{[]string{"job", "label", "set", "missing-job", "team=x"}, 4, "no labels for job missing-job"},
		{[]string{"cron", "label", "set", "cron-billing", "gregale.dev/x=y"}, 1, "belong to Gregale"},
		{[]string{"postgres", "label", "set", "orders"}, 1, "name at least one label"},
		{[]string{"job", "label", "list"}, 1, "usage: gregale job label"},
		{[]string{"job", "list", "-l", "tier in (web"}, 1, "--selector"},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, stderr := captureOutput(t)
			if code := main.Run(tc.args); code != tc.code {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, tc.code, stderr)
			}
			if !strings.Contains(stderr.String(), tc.wantError) {
				t.Errorf("stderr missing %q:\n%s", tc.wantError, stderr)
			}
		})
	}
	for path, w := range rs.writes {
		if path != "/v1/jobs/missing-job" {
			t.Errorf("an invalid change reached %s: %+v", path, w)
		}
	}
}

// -l narrows postgres list, cron list (with or without --app) and job list.
func TestResourceSelectorLists(t *testing.T) {
	newResourceLabelsServer(t)
	tests := []struct {
		args    []string
		want    []string
		notWant []string
	}{
		{[]string{"postgres", "list", "-l", "team=billing"}, []string{"orders"}, []string{"events"}},
		{[]string{"postgres", "list", "-l", "team=nobody"}, []string{"No database matches"}, []string{"orders"}},
		{[]string{"cron", "list", "-l", "team=search"}, []string{"cron-search"}, []string{"cron-billing"}},
		{[]string{"cron", "list", "--app", "billing-web", "-l", "team"}, []string{"cron-billing", "cron-search"}, nil},
		{[]string{"job", "list", "-l", "tier=batch"}, []string{"nightly-report"}, []string{"search-index"}},
		{[]string{"job", "list", "--selector", "!tier", "--all"}, []string{"search-index"}, []string{"nightly-report"}},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr := captureOutput(t)
			if code := main.Run(tc.args); code != 0 {
				t.Fatalf("exit = %d; stderr:\n%s", code, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("output missing %q:\n%s", want, stdout)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(stdout.String(), notWant) {
					t.Errorf("output has %q:\n%s", notWant, stdout)
				}
			}
		})
	}
}
