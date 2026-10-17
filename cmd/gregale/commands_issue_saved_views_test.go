package main

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssueSavedViewCLIUsesLatestDefinitionAndRelativeOwner(t *testing.T) {
	var stored api.IssueSavedView
	methods := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/payments/issues" {
			if r.URL.Query().Get("assignee") != "me" || r.URL.Query().Get("state") != "resolved" || r.URL.Query().Get("tag") != "component=payments" {
				t.Error("apply froze the old view or resolved me locally")
			}
			_ = json.NewEncoder(w).Encode(api.ListIssuesResponse{Items: []api.Issue{{ID: "issue-one", State: "resolved"}}})
			return
		}
		switch r.Method {
		case "PUT":
			var in api.PutIssueSavedViewRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			stored = api.IssueSavedView{Key: "mine", Name: in.Name, Filters: in.Filters}
			_ = json.NewEncoder(w).Encode(stored)
		case "GET":
			_ = json.NewEncoder(w).Encode(stored)
		case "DELETE":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousOut, previousJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
	if cmdIssues([]string{"views", "save", "--app", "payments", "--key", "mine", "--name", "Mine", "--state", "open", "--assignee", "me", "--tag", "component=payments"}) != 0 {
		t.Fatal("save failed")
	}
	stored.Filters.State = "resolved"
	if cmdIssues([]string{"views", "apply", "--app", "payments", "--key", "mine"}) != 0 {
		t.Fatal("apply failed")
	}
	jsonOutput = true
	out.Reset()
	if cmdIssues([]string{"views", "delete", "--app", "payments", "--key", "mine"}) != 0 {
		t.Fatal("delete failed")
	}
	var deletion struct {
		Deleted bool   `json:"deleted"`
		Key     string `json:"key"`
	}
	if err := json.Unmarshal(out.Bytes(), &deletion); err != nil || !deletion.Deleted || deletion.Key != "mine" {
		t.Fatalf("delete JSON: %s %v", out.String(), err)
	}
	if cmdIssues([]string{"views", "apply", "--app", "payments", "--key", "mine", "--state", "open"}) == 0 {
		t.Fatal("apply silently ignored an override")
	}
	if strings.Join(methods, ",") != "PUT /v1/apps/payments/issue-saved-views/mine,GET /v1/apps/payments/issue-saved-views/mine,GET /v1/apps/payments/issues,DELETE /v1/apps/payments/issue-saved-views/mine" {
		t.Fatalf("unexpected view journey: %v", methods)
	}
}
