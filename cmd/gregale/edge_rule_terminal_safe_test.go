package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-091 amendment 2026-10-30 (round 5): an edge-rule event records the
// request's decoded path, so a visitor requesting /%1b]52;c;...%07 planted an
// OSC 52 clipboard write that `gregale edge rule events` and `waf-review`
// printed raw into whoever read them, as logs did before H32. Table output
// escapes control characters; --json keeps the raw bytes.
func TestEdgeRuleEventsAndWAFReviewEscapeControlCharacters(t *testing.T) {
	const planted = "/\x1b]52;c;cHduZWQ=\x07"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/edge-rules/events"):
			_ = json.NewEncoder(w).Encode(api.EdgeRuleEventsResponse{Since: time.Now().Add(-time.Hour),
				Events: []api.EdgeRuleEventResponse{{ID: "1", RuleID: "r1", RuleName: "waf\x1b[2J", Outcome: "blocked",
					OccurredAt: time.Now(), Method: "GET", Host: "api.example.com", Path: planted}}})
		case strings.HasSuffix(r.URL.Path, "/edge-rules/waf-review"):
			_ = json.NewEncoder(w).Encode(api.EdgeWAFReviewResponse{Since: time.Now().Add(-time.Hour),
				Groups: []api.EdgeWAFReviewGroup{{RuleID: "r1", RuleName: "waf", CRSRuleID: 942100, Category: "sqli",
					Assessment: api.EdgeWAFReviewLikelyFalsePositive, Events: 3, Clients: 3, Path: planted, Reason: "many clients"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	for _, args := range [][]string{{"events", "--app", "my-api"}, {"waf-review", "--app", "my-api"}} {
		t.Run(args[0], func(t *testing.T) {
			resetJSONEnv(t)
			var stdout bytes.Buffer
			oldOut := (*main.OsStdout)
			(*main.OsStdout) = &stdout
			defer func() { (*main.OsStdout) = oldOut }()
			if code := main.CmdEdgeRules(args); code != 0 {
				t.Fatalf("%s = %d: %s", args[0], code, stdout.String())
			}
			out := stdout.String()
			if strings.ContainsAny(out, "\x1b\x07") {
				t.Fatalf("output carries raw control characters: %q", out)
			}
			if !strings.Contains(out, `\x1b]52;c;`) {
				t.Fatalf("output does not show the escaped sequence: %q", out)
			}
		})
	}
}
