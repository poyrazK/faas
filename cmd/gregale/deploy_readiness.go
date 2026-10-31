package main

import (
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/deploydiff"
)

// Keep the stable diff stdout and JSON contracts separate from human guidance.
func renderDeployReadiness(w io.Writer, opts diffCLIOptions, diff deploydiff.Diff) {
	state := "No blocking findings reported"
	var first *deploydiff.Break
	for i := range diff.Breaks {
		if diff.Breaks[i].Severity != deploydiff.SeverityWarn {
			first = &diff.Breaks[i]
			state = "Blocking findings reported"
			break
		}
	}
	if first == nil && len(diff.Breaks) > 0 {
		first = &diff.Breaks[0]
		state = "Review warnings before deployment"
	}
	_, _ = fmt.Fprintf(w, "Deployment readiness: %s\n  app: %s\n  connection profile: %s\n", state, oneLine(opts.Slug), oneLine(currentProfile()))
	_, _ = fmt.Fprintln(w, "  target: default scope (named --environment is not supported by this preview)")
	if runtime := deployPreflightRuntime(opts.BuildPlan); runtime != "" {
		_, _ = fmt.Fprintf(w, "  inferred runtime: %s\n", oneLine(runtime))
	} else if opts.Runtime != "" {
		_, _ = fmt.Fprintf(w, "  runtime: %s\n", oneLine(opts.Runtime))
	} else {
		_, _ = fmt.Fprintln(w, "  runtime: not established by this preview")
	}
	if opts.BuildPlan != nil && opts.BuildPlan.Entrypoint != "" {
		_, _ = fmt.Fprintf(w, "  inferred start: %s\n", compactInspectFailureText(opts.BuildPlan.Entrypoint))
	} else if opts.BuildPlan != nil && opts.BuildPlan.Handler != "" {
		_, _ = fmt.Fprintf(w, "  handler: %s\n", oneLine(opts.BuildPlan.Handler))
	} else {
		_, _ = fmt.Fprintln(w, "  start: not established by this preview")
	}
	if first != nil {
		_, _ = fmt.Fprintf(w, "  First finding: %s\n  Reason: %s\n", oneLine(first.Code), compactInspectFailureText(first.Reason))
		if first.Field != "" {
			_, _ = fmt.Fprintf(w, "  Review first: %s in the detailed diff below.\n", oneLine(first.Field))
		} else {
			_, _ = fmt.Fprintln(w, "  Review first: this finding in the detailed diff below.")
		}
	}
	if diff.HasBlockingBreaks() && opts.Lenient {
		_, _ = fmt.Fprintln(w, "  --lenient changes the preview exit code; blocking findings still need review.")
	}
	_, _ = fmt.Fprintln(w, "This is a read-only preview, not a guarantee that a build or runtime will succeed.")
	_, _ = fmt.Fprintln(w)
}

func renderDoctorReadiness(w io.Writer, report doctorReport) {
	var first *doctorCheck
	for _, status := range []string{"error", "warn"} {
		for i := range report.Checks {
			if report.Checks[i].Status == status {
				first = &report.Checks[i]
				break
			}
		}
		if first != nil {
			break
		}
	}
	if first == nil {
		// The report's closing line already says there are no findings and
		// which checks were skipped; a second summary up front repeats it.
		return
	}
	_, _ = fmt.Fprintf(w, "Local checks: %s finding — %s\n", first.Status, oneLine(first.Name))
	if cause := firstInspectFailureText(first.Why, first.Hint); cause != "" {
		_, _ = fmt.Fprintf(w, "  Reason: %s\n", cause)
	}
	if fix := firstInspectFailureText(first.Fix, first.Hint); fix != "" {
		_, _ = fmt.Fprintf(w, "  Fix first: %s\n", fix)
	}
	if len(first.Sources) > 0 {
		_, _ = fmt.Fprintf(w, "  First source: %s\n", compactInspectFailureText(first.Sources[0]))
	}
	_, _ = fmt.Fprintln(w)
}
