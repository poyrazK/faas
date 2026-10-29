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

// adr: 20261020105001 — `edge-rules review` reads the rules and the 7-day hourly
// series and prints ready, collecting and quiet rules with the command to
// act on each; `edge-rules stats` draws each rule's trend.
func TestEdgeRulesReviewCommand(t *testing.T) {
	main.ResetJSONOut(t)
	now := time.Now().UTC()
	since := now.Add(-7 * 24 * time.Hour).Truncate(time.Hour)
	hours := int(now.Truncate(time.Hour).Sub(since)/time.Hour) + 1
	readySeries := make([]int64, hours)
	readySeries[hours-1], readySeries[hours-30] = 10, 30
	rules := []api.EdgeRuleResponse{
		{ID: "r-ready", Kind: "ip", Name: "Ready rule", Mode: api.EdgeRuleModeLog, Enabled: true, UpdatedAt: now.Add(-72 * time.Hour)},
		{ID: "r-fresh", Kind: "ip", Name: "Fresh rule", Mode: api.EdgeRuleModeLog, Enabled: true, UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "r-dead", Kind: "ip", Mode: api.EdgeRuleModeEnforce, Enabled: true, UpdatedAt: now.Add(-10 * 24 * time.Hour)},
		{ID: "r-off", Kind: "ip", Name: "Off rule", Mode: api.EdgeRuleModeLog, UpdatedAt: now.Add(-72 * time.Hour)},
	}
	stats := api.EdgeRuleStatsResponse{Window: "7d", Since: since, Rules: []api.EdgeRuleHitStatsResponse{{RuleID: "r-ready", Logged: 40, Series: readySeries}}}
	var sawStatsQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/demo/edge-rules":
			_ = json.NewEncoder(w).Encode(rules)
		case "/v1/apps/demo/edge-rules/stats":
			sawStatsQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(stats)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_API", srv.URL)
	var buf bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &buf
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	if code := main.CmdEdgeRules([]string{"review", "--app", "demo"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(sawStatsQuery, "series=true") || !strings.Contains(sawStatsQuery, "window=7d") {
		t.Fatalf("stats query = %q", sawStatsQuery)
	}
	out := buf.String()
	for _, want := range []string{
		"READY       Ready rule (r-ready)", "Matched 40 requests in log mode", "gregale edge rule update r-ready --mode enforce",
		"COLLECTING  Fresh rule (r-fresh)", "QUIET       ip rule (r-dead)", "gregale edge rule delete r-dead",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Off rule") {
		t.Errorf("review lists a disabled rule:\n%s", out)
	}
	if strings.Index(out, "READY") > strings.Index(out, "COLLECTING") || strings.Index(out, "COLLECTING") > strings.Index(out, "QUIET") {
		t.Errorf("order is not ready, collecting, quiet:\n%s", out)
	}

	buf.Reset()
	if code := main.CmdEdgeRules([]string{"stats", "--app", "demo", "--window", "7d"}); code != 0 {
		t.Fatalf("stats exit = %d", code)
	}
	if !strings.Contains(buf.String(), "TREND") || !strings.Contains(buf.String(), "█") {
		t.Fatalf("stats output has no trend:\n%s", buf.String())
	}
	if got := main.EdgeRuleTrend("24h", []int64{0, 4, 8}); got != strings.Repeat("▁", 21)+"▁▅█" {
		t.Fatalf("24h trend = %q", got)
	}
	if got := main.EdgeRuleTrend("1h", []int64{3}); got != "-" {
		t.Fatalf("1h trend = %q", got)
	}
}
