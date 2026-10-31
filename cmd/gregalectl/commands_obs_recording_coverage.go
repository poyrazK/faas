package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
)

func cmdObsRecordingCoverage(args []string) int {
	fs := flag.NewFlagSet(subObsRecordingCoverage, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	selected := ""
	fs.Func("expected-set", "explicit expected set (gregale-core-v1 or gregale-full-v1)", func(value string) error {
		if selected != "" || !obsrulecatalog.KnownSetID(value) {
			return errors.New("expected-set must be supplied once with a known nonempty ID")
		}
		selected = value
		return nil
	})
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs recording-coverage: no positional arguments and a positive timeout required")
		return 2
	}
	cred, ok := resolveObsCredential("obs recording-coverage", *token)
	if !ok {
		return 2
	}
	path := operatorobs.RecordingCoveragePath
	if selected != "" {
		path += "?" + url.Values{"expected_set": {selected}}.Encode()
	}
	client := &http.Client{Timeout: *timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var out api.ObsRecordingCoverage
	if err := fetchObsBundleSection(context.Background(), client, apidBase()+path, cred, subObsRecordingCoverage, &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if !validObsRecordingCoverage(out, selected) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs recording-coverage: invalid response")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
	writeObsRecordingCoverage(osStdout, out)
	return 0
}

func writeObsRecordingCoverage(w io.Writer, out api.ObsRecordingCoverage) {
	writeDefinitionCoverage(w, recordingCoverageBase(out), "recording_coverage")
	if out.Dependencies == nil {
		return
	}
	d := out.Dependencies
	_, _ = fmt.Fprintf(w, "dependency_mapping=%s scope=%s alert_set_revision=%s total_alerts=%d uncertain_alerts=%d uncertain_preview=%d uncertain_truncated=%t\n", d.State, d.Scope, d.AlertSetRevision, d.TotalAlerts, d.UncertainAlerts.TotalItems, len(d.UncertainAlerts.Items), d.UncertainAlerts.Truncated)
	for _, a := range d.UncertainAlerts.Items {
		_, _ = fmt.Fprintf(w, "  uncertain_alert=%s file=%s group=%s rule=%s\n", a.ID, a.FileName, a.GroupName, a.RuleName)
	}
	for _, r := range out.Items {
		_, _ = fmt.Fprintf(w, "  recording=%s known_dependent_alerts=%d preview=%d truncated=%t\n", r.Comparison.ID, r.DependentAlerts.TotalItems, len(r.DependentAlerts.Items), r.DependentAlerts.Truncated)
		for _, a := range r.DependentAlerts.Items {
			_, _ = fmt.Fprintf(w, "    alert=%s file=%s group=%s rule=%s\n", a.ID, a.FileName, a.GroupName, a.RuleName)
		}
	}
	_, _ = fmt.Fprintln(w, "dependencies=expected_catalog_syntax runtime_impact=unverified")
}
