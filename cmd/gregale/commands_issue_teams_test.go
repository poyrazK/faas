package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
)

func TestIssueTeamsCLIInboxAndCodeownersPreview(t *testing.T) {
	id, member := uuid.NewString(), uuid.NewString()
	var stored api.IssueTeam
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/payments/issues" {
			if r.URL.Query().Get("team") != id || r.URL.Query().Get("state") != "open" {
				t.Error("team inbox lost filter")
			}
			_ = json.NewEncoder(w).Encode(api.ListIssuesResponse{Items: []api.Issue{{ID: uuid.NewString(), AssigneeTeamID: id}}})
			return
		}
		switch r.Method {
		case "PUT":
			var in api.PutIssueTeamRequest
			_ = json.NewDecoder(r.Body).Decode(&in)
			stored = api.IssueTeam{ID: id, Key: "payments", Name: in.Name, MemberAccountIDs: in.MemberAccountIDs}
			_ = json.NewEncoder(w).Encode(stored)
		case "GET":
			_ = json.NewEncoder(w).Encode(stored)
		case "DELETE":
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	var out bytes.Buffer
	(*main.OsStdout), (*main.JsonOutput) = &out, true
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })
	if main.CmdIssues([]string{"teams", "save", "--app", "payments", "--key", "payments", "--name", "Payments", "--members", member}) != 0 || main.CmdIssues([]string{"teams", "inbox", "--app", "payments", "--key", "payments"}) != 0 {
		t.Fatal("team management journey failed")
	}
	dir := t.TempDir()
	file, bindings := filepath.Join(dir, "CODEOWNERS"), filepath.Join(dir, "owners.json")
	if err := os.WriteFile(file, []byte("/src/payments/ @org/payments\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bindings, []byte(`{"@org/payments":{"team_id":"`+id+`"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	before := requests
	if main.CmdIssues([]string{"codeowners", "preview", "--file", file, "--bindings-file", bindings, "--source-root", "/app"}) != 0 {
		t.Fatal("offline preview failed")
	}
	var compiled issues.CodeownersImport
	if err := json.Unmarshal(out.Bytes(), &compiled); err != nil || len(compiled.Rules) != 1 || compiled.Rules[0].AssigneeTeamID != id {
		t.Fatalf("preview: %s %v", out.String(), err)
	}
	if main.CmdIssues([]string{"codeowners", "apply", "--file", file, "--bindings-file", bindings, "--source-root", "/app", "--app", "payments"}) == 0 {
		t.Fatal("implicit policy replacement allowed")
	}
	if requests != before {
		t.Fatal("preview or unconfirmed replacement contacted API")
	}
}
