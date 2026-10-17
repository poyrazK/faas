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
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsInputHealth(args []string) int {
	fs := flag.NewFlagSet(subObsInputHealth, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	role := ""
	fs.Func("expected-role", "explicit expectation: single-box-v1 or control-plane-v1", func(value string) error {
		if role != "" {
			return errors.New("expected-role must be supplied once")
		}
		selected, err := operatorobs.InputHealthSelection(url.Values{"expected_role": {value}}.Encode(), false)
		if err != nil {
			return errors.New("expected-role must be a known nonempty role")
		}
		role = selected
		return nil
	})
	token := fs.String("admin-token", "", "admin bearer (default: $FAAS_ADMIN_TOKEN)")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs input-health: no positional arguments and a positive timeout required")
		return 2
	}
	if *token == "" {
		*token = os.Getenv("FAAS_ADMIN_TOKEN")
	}
	if *token == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs input-health: admin token required")
		return 2
	}
	path := operatorobs.InputHealthPath
	if role != "" {
		path += "?" + url.Values{"expected_role": {role}}.Encode()
	}
	client := &http.Client{Timeout: *timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var out api.ObsInputHealth
	if err := fetchObsBundleSection(context.Background(), client, apidBase()+path, *token, subObsInputHealth, &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if !validObsInputHealth(out, role) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs input-health: invalid response")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
	writeObsInputHealth(osStdout, out)
	return 0
}

func writeObsInputHealth(w io.Writer, out api.ObsInputHealth) {
	_, _ = fmt.Fprintf(w, "input_health=%s reason=%s expected_role=%s scope=%s generated_at=%s max_age_seconds=%d lookback_seconds=%d\n", out.State, out.Reason, out.ExpectedRole, out.Scope, out.GeneratedAt.Format(time.RFC3339), out.MaxAgeSeconds, out.LookbackSeconds)
	for _, role := range out.AvailableRoles {
		_, _ = fmt.Fprintf(w, "available_role=%s\n", role)
	}
	for _, job := range out.Jobs {
		e := job.Expectation
		_, _ = fmt.Fprintf(w, "  job=%s state=%s expectation=%s expectation_state=%s reason=%s minimum_targets=%s invalid_targets=%s\n", job.Job, job.State, e.Source, e.State, e.Reason, ruleHealthCount(e.MinimumTargets), ruleHealthCount(e.InvalidTargets))
		_, _ = fmt.Fprintf(w, "    targets=%s reason=%s", job.Targets.State, job.Targets.Reason)
		if c := job.Targets.Counts; c != nil {
			_, _ = fmt.Fprintf(w, " observed=%d fresh_up=%d down=%d stale=%d missing_minimum=%s extra=%s", c.Observed, c.FreshUp, c.Down, c.Stale, ruleHealthCount(c.MissingMinimum), ruleHealthCount(c.Extra))
		}
		_, _ = fmt.Fprintln(w)
		for _, m := range job.Metrics {
			_, _ = fmt.Fprintf(w, "    metric=%s state=%s reason=%s", m.Metric, m.State, m.Reason)
			if c := m.Counts; c != nil {
				_, _ = fmt.Fprintf(w, " expected=%d fresh=%d missing=%d stale=%d", c.Expected, c.Fresh, c.Missing, c.Stale)
			}
			_, _ = fmt.Fprintln(w)
		}
	}
	_, _ = fmt.Fprintln(w, "metric_scope=fresh_up_targets readiness_value=unassessed rule_evaluation=unassessed")
}
