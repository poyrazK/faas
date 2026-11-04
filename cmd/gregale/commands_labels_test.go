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

// labelsServer serves apps with labels. Like an apid without labels, it
// ignores ?selector= on the app list, so the CLI's own match is what the
// tests see. It records every label write.
type labelsServer struct {
	mu     sync.Mutex
	apps   []api.AppResponse
	writes []api.LabelChange
}

func newLabelsServer(t *testing.T, apps ...api.AppResponse) *labelsServer {
	t.Helper()
	ls := &labelsServer{apps: apps}
	srv := httptest.NewServer(http.HandlerFunc(ls.serve))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_labels")
	return ls
}

func (ls *labelsServer) serve(w http.ResponseWriter, r *http.Request) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/v1/apps":
		_ = json.NewEncoder(w).Encode(ls.apps)
		return
	case r.Method == http.MethodGet && path == "/v1/domains":
		_ = json.NewEncoder(w).Encode([]api.CustomDomainResponse{
			{Domain: "api.billing.example.com", AppID: "id-billing-api"},
			{Domain: "search.example.com", AppID: "id-search-web"},
		})
		return
	}
	for i, app := range ls.apps {
		switch {
		case r.Method == http.MethodGet && (path == "/v1/apps/"+app.Slug):
			_ = json.NewEncoder(w).Encode(app)
			return
		case r.Method == http.MethodGet && path == "/v1/apps/"+app.Slug+"/status":
			_ = json.NewEncoder(w).Encode(api.ResourceStatus{Kind: "app", Name: app.Slug, ObservedAt: "2026-10-31T15:00:00Z",
				Conditions: []api.Condition{{Type: "Ready", Status: "True", Reason: "idle", Message: "Ready."}}})
			return
		case r.Method == http.MethodPatch && path == "/v1/apps/"+app.Slug+"/labels":
			var change api.LabelChange
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &change)
			ls.writes = append(ls.writes, change)
			next := maps.Clone(app.Labels)
			if next == nil {
				next = map[string]string{}
			}
			for _, k := range change.Remove {
				delete(next, k)
			}
			maps.Copy(next, change.Set)
			ls.apps[i].Labels = next
			_ = json.NewEncoder(w).Encode(api.LabelsResponse{Labels: next})
			return
		}
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found"}`))
}

func labelledApps() []api.AppResponse {
	return []api.AppResponse{
		{ID: "id-billing-web", Slug: "billing-web", Status: "active", Labels: map[string]string{"team": "billing", "tier": "web"}},
		{ID: "id-billing-api", Slug: "billing-api", Status: "active", Labels: map[string]string{"team": "billing", "tier": "api"}},
		{ID: "id-search-web", Slug: "search-web", Status: "active", Labels: map[string]string{"team": "search", "tier": "web"}},
	}
}

// app label set/unset/list send one write per command and print the labels
// the app carries after it.
func TestAppLabelCommands(t *testing.T) {
	ls := newLabelsServer(t, labelledApps()...)
	tests := []struct {
		args []string
		want []string
	}{
		{[]string{"app", "label", "set", "billing-web", "canary=", "owner=ops"}, []string{"canary", "owner", "ops", "team", "billing"}},
		{[]string{"app", "label", "unset", "billing-web", "canary"}, []string{"owner", "team"}},
		{[]string{"app", "billing-web", "label", "list"}, []string{"KEY", "owner", "ops"}},
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
	if len(ls.writes) != 2 || ls.writes[0].Set["canary"] != "" || ls.writes[0].Set["owner"] != "ops" || ls.writes[1].Remove[0] != "canary" {
		t.Fatalf("writes = %+v", ls.writes)
	}
}

// Invalid labels and selectors are refused before any request.
func TestAppLabelCommandsRefuseInvalidInput(t *testing.T) {
	ls := newLabelsServer(t, labelledApps()...)
	tests := []struct {
		args      []string
		wantError string
	}{
		{[]string{"app", "label", "set", "billing-web", "team"}, "set takes KEY=VALUE"},
		{[]string{"app", "label", "set", "billing-web", "gregale.dev/x=y"}, "belong to Gregale"},
		{[]string{"app", "label", "set", "billing-web", "bad key=v"}, "bad key"},
		{[]string{"app", "label", "unset", "billing-web"}, "name at least one label"},
		{[]string{"app", "list", "-l", "tier in (web"}, "--selector"},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, stderr := captureOutput(t)
			if code := main.Run(tc.args); code == 0 {
				t.Fatalf("exit 0; want a refusal")
			}
			if !strings.Contains(stderr.String(), tc.wantError) {
				t.Errorf("stderr missing %q:\n%s", tc.wantError, stderr)
			}
		})
	}
	if len(ls.writes) != 0 {
		t.Fatalf("an invalid change was sent: %+v", ls.writes)
	}
}

// -l narrows app list, domain list and app wait. The server here ignores the
// selector, so the results show the CLI's own match.
func TestSelectorCommands(t *testing.T) {
	newLabelsServer(t, labelledApps()...)
	tests := []struct {
		args    []string
		code    int
		want    []string
		notWant []string
	}{
		{[]string{"app", "list", "-l", "team=billing"}, 0, []string{"billing-web", "billing-api"}, []string{"search-web"}},
		{[]string{"apps", "--selector", "tier in (web),team!=billing"}, 0, []string{"search-web"}, []string{"billing-web", "billing-api"}},
		{[]string{"app", "list", "-l", "!tier"}, 0, []string{"No app matches"}, []string{"billing-web"}},
		{[]string{"domain", "list", "-l", "team=billing"}, 0, []string{"api.billing.example.com"}, []string{"search.example.com"}},
		{[]string{"app", "wait", "-l", "tier=web", "--poll-interval", "1s"}, 0, []string{"app billing-web", "app search-web"}, []string{"billing-api"}},
		{[]string{"app", "wait", "-l", "team=nobody", "--poll-interval", "1s"}, 4, nil, nil},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr := captureOutput(t)
			if code := main.Run(tc.args); code != tc.code {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, tc.code, stderr)
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
