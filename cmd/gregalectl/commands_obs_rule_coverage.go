package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
)

func cmdObsRuleCoverage(args []string) int {
	fs := flag.NewFlagSet(subObsRuleCoverage, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	selected := ""
	fs.Func("expected-set", "explicit expected set (gregale-core-v1 or gregale-full-v1)", func(value string) error {
		if selected != "" || !obsrulecatalog.KnownSetID(value) {
			return errors.New("expected-set must be supplied once with a known nonempty ID")
		}
		selected = value
		return nil
	})
	token := fs.String("admin-token", "", "admin bearer (default: $FAAS_ADMIN_TOKEN)")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs rule-coverage: no positional arguments and a positive timeout required")
		return 2
	}
	if *token == "" {
		*token = os.Getenv("FAAS_ADMIN_TOKEN")
	}
	if *token == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs rule-coverage: admin token required")
		return 2
	}
	path := operatorobs.RuleCoveragePath
	if selected != "" {
		path += "?" + url.Values{"expected_set": {selected}}.Encode()
	}
	client := &http.Client{Timeout: *timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var out api.ObsRuleCoverage
	if err := fetchObsBundleSection(context.Background(), client, apidBase()+path, *token, subObsRuleCoverage, &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if !validObsRuleCoverage(out, selected) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs rule-coverage: invalid response")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
	writeObsRuleCoverage(osStdout, out)
	return 0
}

func writeObsRuleCoverage(w io.Writer, out api.ObsRuleCoverage) {
	writeDefinitionCoverage(w, out, "rule_coverage")
}

func writeDefinitionCoverage(w io.Writer, out api.ObsRuleCoverage, name string) {
	_, _ = fmt.Fprintf(w, "%s=%s reason=%s scope=%s selection=%s generated_at=%s\n", name, out.State, out.Reason, out.Scope, out.SelectionSource, out.GeneratedAt.UTC().Format(time.RFC3339Nano))
	if out.ExpectedSet != nil {
		s := out.ExpectedSet
		_, _ = fmt.Fprintf(w, "expected_set=%s revision=%s files=%s expected_rules=%d\n", s.ID, s.Revision, strings.Join(s.Files, ","), s.ExpectedRules)
	}
	if out.Totals == nil {
		_, _ = fmt.Fprintln(w, "totals=unknown")
		for _, s := range out.AvailableSets {
			_, _ = fmt.Fprintf(w, "available_set=%s revision=%s files=%s expected_rules=%d\n", s.ID, s.Revision, strings.Join(s.Files, ","), s.ExpectedRules)
		}
		return
	}
	t := out.Totals
	_, _ = fmt.Fprintf(w, "expected=%d observed=%d matching=%d missing=%d changed=%d ambiguous=%d unknown=%d additional=%d invalid_observed_rules=%d invalid_observed_groups=%d preview=%d total_items=%d truncated=%t\n", t.ExpectedRules, t.ObservedRules, t.Matching, t.Missing, t.Changed, t.Ambiguous, t.Unknown, t.AdditionalRules, t.InvalidObservedRules, t.InvalidObservedGroups, len(out.Items), out.TotalItems, out.Truncated)
	for _, r := range out.Items {
		_, _ = fmt.Fprintf(w, "  id=%s file=%s group=%s rule=%s state=%s reason=%s candidates=%d expected_fingerprint=%s observed_fingerprint=%s changed_fields=%s\n", r.ID, r.FileName, r.GroupName, r.RuleName, r.State, r.Reason, r.ObservedCandidates, r.ExpectedFingerprint, r.ObservedFingerprint, strings.Join(r.ChangedFields, ","))
	}
	_, _ = fmt.Fprintln(w, "coverage=selected_definitions evaluation_metric_and_notification_health=unverified")
}
