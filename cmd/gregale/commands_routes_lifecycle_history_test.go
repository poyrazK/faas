package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRoutesLifecycleHistory(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/apps/api/route-lifecycle/history" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("before") != "10" {
			t.Error("request", r.URL.String())
		}
		json.NewEncoder(w).Encode(api.RouteLifecycleHistoryPage{AppID: "app", NextCursor: "8", Entries: []api.RouteLifecycleHistoryEntry{{ID: "9", DeploymentID: "dep", ReviewedAt: time.Now().UTC(), Outcome: "blocked", Decision: api.RouteGateDecision{Reasons: []string{"lifecycle_successor_changed_requires_review"}}, Approvals: []api.RouteLifecycleHistoryApproval{{ID: "receipt", Status: "expired", StatusReason: "approval_expired"}}}}})
	}))
	defer ts.Close()
	t.Setenv("FAAS_API", ts.URL)
	oldOut, oldErr := osStdout, osStderr
	var out, errOut bytes.Buffer
	osStdout = &out
	osStderr = &errOut
	t.Cleanup(func() { osStdout = oldOut; osStderr = oldErr })
	if code := cmdRoutesLifecycle([]string{"history", "api", "--limit", "2", "--before", "10"}); code != 0 {
		t.Fatal(code, errOut.String())
	}
	if !strings.Contains(out.String(), "lifecycle_successor_changed_requires_review") || !strings.Contains(out.String(), "approval_expired") || !strings.Contains(out.String(), "--before 8") {
		t.Fatal(out.String())
	}
	out.Reset()
	jsonOutput = true
	if code := cmdRoutesLifecycleHistory([]string{"api", "--limit", "2", "--before", "10"}); code != 0 {
		t.Fatal(code, errOut.String())
	}
	var page api.RouteLifecycleHistoryPage
	if json.Unmarshal(out.Bytes(), &page) != nil || page.NextCursor != "8" {
		t.Fatal(out.String())
	}
	for _, args := range [][]string{{"api", "--limit", "0"}, {"api", "--limit", "21"}, {"api", "--before", "01"}, {"api", "--before", "9223372036854775808"}, {"api", "extra"}} {
		if code := cmdRoutesLifecycleHistory(args); code == 0 {
			t.Fatal("invalid", args)
		}
	}
	if calls != 2 {
		t.Fatal("invalid input sent requests", calls)
	}
}
