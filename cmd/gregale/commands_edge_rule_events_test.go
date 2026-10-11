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

func edgeRuleEventsServer(t *testing.T, gotQuery *string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(api.EdgeRuleEventsResponse{
			Since: time.Now().Add(-time.Hour),
			Events: []api.EdgeRuleEventResponse{{ID: "1", RuleID: "r1", RuleName: "scrapers", Outcome: "logged",
				OccurredAt: time.Now(), Method: "GET", Host: "api.example.com", Path: "/x", ClientIP: "203.0.113.9", Country: "DE"}},
			NextCursor: "123-1",
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
}

func TestCmdEdgeRulesEvents(t *testing.T) {
	resetJSONEnv(t)
	var query string
	edgeRuleEventsServer(t, &query)
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdEdgeRules([]string{"events", "--app", "my-api", "--outcome", "logged", "--since", "7d"}); code != 0 {
		t.Fatalf("events = %d", code)
	}
	for _, want := range []string{"outcome=logged", "since=7d", "limit=50"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q missing %s", query, want)
		}
	}
	for _, want := range []string{"scrapers", "203.0.113.9", "api.example.com/x", "--cursor 123-1"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output missing %q:\n%s", want, stdout.String())
		}
	}
	if code := cmdEdgeRules([]string{"events"}); code != 1 {
		t.Fatalf("events without --app = %d, want 1", code)
	}
}

func TestCmdEdgeRulesEvents_JSON(t *testing.T) {
	resetJSONEnv(t)
	jsonOutput = true
	defer func() { jsonOutput = false }()
	var query string
	edgeRuleEventsServer(t, &query)
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdEdgeRules([]string{"events", "--app", "my-api"}); code != 0 {
		t.Fatalf("events --json = %d", code)
	}
	if !strings.Contains(stdout.String(), `"next_cursor"`) {
		t.Fatalf("json output = %q", stdout.String())
	}
}
