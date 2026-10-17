package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestIssueLifecycleAlertsCLI(t *testing.T) {
	requests := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			var in api.UpdateIssueLifecycleAlertPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ExpectedRevision == nil || *in.ExpectedRevision != 2 || in.CooldownSeconds != 1800 || len(in.Environments) != 1 || in.Environments[0] != "production" {
				t.Errorf("save=%+v err=%v", in, err)
			}
			_, _ = w.Write([]byte(`{"revision":3,"environments":["production"],"cooldown_seconds":1800}`))
		case http.MethodPost:
			var in api.PreviewIssueLifecycleAlertRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.IssueID == "" || in.Event != "regressed" {
				t.Errorf("preview=%+v err=%v", in, err)
			}
			_, _ = w.Write([]byte(`{"issue_id":"28ad5060-28e6-4ba5-ac6a-9990691c11c3","event":"regressed","environment":"application","policy_revision":3,"allowed":false,"reason":"environment_excluded"}`))
		default:
			_, _ = w.Write([]byte(`{"revision":2,"environments":[],"cooldown_seconds":0}`))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previous := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = previous })
	for _, args := range [][]string{
		{"get", "--app", "payments"},
		{"save", "--app", "payments", "--expected-revision", "2", "--environments", "production", "--cooldown-seconds", "1800"},
		{"preview", "--app", "payments", "--issue", "28ad5060-28e6-4ba5-ac6a-9990691c11c3", "--environments", "production"},
	} {
		if cmdIssues(append([]string{"lifecycle-alerts"}, args...)) != 0 {
			t.Fatal(args)
		}
	}
	if len(requests) != 3 || requests[0] != "GET /v1/apps/payments/issue-lifecycle-alert-policy" || requests[2] != "POST /v1/apps/payments/issue-lifecycle-alert-policy/preview" || !strings.Contains(out.String(), "environment_excluded") {
		t.Fatal(requests, out.String())
	}
	for _, args := range [][]string{
		{"save", "--app", "payments"},
		{"get", "--app", "payments", "--cooldown-seconds", "1"},
		{"preview", "--app", "payments", "--issue", "bad"},
		{"save", "--app", "payments", "--expected-revision", "2", "--environments", "prod*"},
		{"save", "--app", "payments", "--expected-revision", "2", "--cooldown-seconds", "604801"},
	} {
		if cmdIssues(append([]string{"lifecycle-alerts"}, args...)) == 0 {
			t.Fatal("invalid input accepted", args)
		}
	}
	if len(requests) != 3 {
		t.Fatal("invalid input contacted API")
	}
}
