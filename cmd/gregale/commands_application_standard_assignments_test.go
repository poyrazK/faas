package main

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrgStandardAssignmentInventoryCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "fixture")
	id := uuid.NewString()
	alias := strings.ToUpper(strings.ReplaceAll(id, "-", ""))
	for _, action := range []string{"list", "show"} {
		t.Run(action, func(t *testing.T) {
			called := 0
			path := "/v1/orgs/acme/application-standard-assignments"
			args := []string{"standards", "assignments", action, "--org", "acme"}
			if action == "list" {
				args = append(args, "--after", alias, "--limit", "1")
				path += "?after=" + id + "&limit=1"
			} else {
				args = append(args, "--id", alias)
				path += "/" + id
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called++
				if r.Method != http.MethodGet || r.URL.RequestURI() != path || r.Header.Get("Authorization") != "Bearer fixture" {
					t.Errorf("inventory request: %s %s", r.Method, r.URL.RequestURI())
				}
				row := api.ApplicationStandardAssignment{ID: id, Active: false, Revision: 3, AdmissionVersion: 2}
				if action == "list" {
					writeJSONTestStatus(w, http.StatusOK, api.ApplicationStandardAssignmentList{Assignments: []api.ApplicationStandardAssignment{row}, NextPageAfter: id})
				} else {
					writeJSONTestStatus(w, http.StatusOK, row)
				}
			}))
			defer ts.Close()
			t.Setenv("FAAS_API", ts.URL)
			output, restore := captureStdout(t)
			defer restore()
			if code := cmdOrgs(args); code != 0 || called != 1 || !json.Valid([]byte(output.String())) || !strings.Contains(output.String(), `"active": false`) || !strings.Contains(output.String(), `"revision": 3`) {
				t.Fatalf("inventory CLI exit=%d calls=%d output=%s", code, called, output.String())
			}
		})
	}
}

func TestOrgStandardAssignmentInventoryCLIRefusalsBeforeNetwork(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "fixture")
	called := 0
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))
	defer ts.Close()
	t.Setenv("FAAS_API", ts.URL)
	for _, args := range [][]string{nil, {"unknown"}, {"list"}, {"show", "--org", "acme"}, {"show", "--org", "acme", "--id", "bad"}, {"show", "--org", "acme", "--id", uuid.Nil.String()}, {"list", "--org", "acme", "--after", "bad"}, {"list", "--org", "acme", "--after", uuid.Nil.String()}, {"list", "--org", "acme", "--limit", "0"}, {"list", "--org", "acme", "--limit", "101"}, {"list", "--org", "acme", "extra"}} {
		if code := cmdOrgs(append([]string{"standards", "assignments"}, args...)); code == 0 || called != 0 {
			t.Fatalf("invalid inventory arguments reached network: %v exit=%d calls=%d", args, code, called)
		}
	}
}
