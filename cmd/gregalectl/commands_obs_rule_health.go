package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsRuleHealth(args []string) int {
	fs := flag.NewFlagSet(subObsRuleHealth, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs rule-health: no positional arguments and a positive timeout required")
		return 2
	}
	cred, ok := resolveObsCredential("obs rule-health", *token)
	if !ok {
		return 2
	}
	var out api.ObsRuleHealthResponse
	client := &http.Client{Timeout: *timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err := fetchObsBundleSection(context.Background(), client, apidBase()+operatorobs.RuleHealthPath, cred, subObsRuleHealth, &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if !validObsRuleHealth(out) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs rule-health: invalid response")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
	writeObsRuleHealth(osStdout, out)
	return 0
}

func writeObsRuleHealth(w io.Writer, out api.ObsRuleHealthResponse) {
	_, _ = fmt.Fprintf(w, "rule_health=%s reason=%s scope=%s generated_at=%s preview=%d total_items=%d truncated=%t\n", out.State, out.Reason, out.Scope, out.GeneratedAt.Format(time.RFC3339), len(out.Items), out.TotalItems, out.Truncated)
	if out.Totals == nil {
		_, _ = fmt.Fprintln(w, "totals=unknown")
		return
	}
	t := out.Totals
	_, _ = fmt.Fprintf(w, "groups=%d rules=%d healthy=%d failed=%d stale=%d invalid_clock=%d unavailable=%d invalid=%d invalid_groups=%d\n", t.Groups, t.Rules, t.Healthy, t.Failed, t.Stale, t.InvalidClock, t.Unavailable, t.Invalid, t.InvalidGroups)
	for _, item := range out.Items {
		age := "unknown"
		if item.EvaluationAgeSeconds != nil {
			age = strconv.FormatInt(*item.EvaluationAgeSeconds, 10)
		}
		_, _ = fmt.Fprintf(w, "  kind=%s id=%s group=%s group_id=%s file=%s rule=%s state=%s reported_state=%s age_seconds=%s pending=%s firing=%s issues=%s runbook_url=%s\n", item.Kind, item.ID, item.GroupName, item.GroupID, item.FileName, item.RuleName, item.State, item.ReportedState, age, ruleHealthCount(item.Pending), ruleHealthCount(item.Firing), strings.Join(item.Issues, ","), item.RunbookURL)
	}
	_, _ = fmt.Fprintln(w, "coverage=current_loaded_rules preview_is_bounded historical_failure_cause=unknown expected_inventory=unobserved")
}

func ruleHealthCount(value *int) string {
	if value == nil {
		return "unknown"
	}
	return strconv.Itoa(*value)
}
