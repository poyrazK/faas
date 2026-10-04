package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

func cmdRoutesImpact(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-impact", "fail-on-incomplete")
	fs := newFlagSet("routes impact", flag.ContinueOnError)
	base := fs.String("base", "", "baseline Git revision (required)")
	head := fs.String("head", "", "candidate Git revision (defaults to working tree)")
	path := fs.String("path", ".", "application source directory inside a Git repository")
	entrypoint := fs.String("entrypoint", "", "FastAPI module:variable (inferred when exactly one exists)")
	format := fs.String("format", "text", "text or markdown")
	output := fs.String("out", "", "save the JSON report to a new file")
	failImpact := fs.Bool("fail-on-impact", false, "exit 1 when routes were added, removed, or may be affected")
	failIncomplete := fs.Bool("fail-on-incomplete", false, "exit 1 when static analysis is incomplete")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) > 1 || (len(positional) == 1 && !validCLISlug(positional[0])) || *base == "" || (*format != "text" && *format != "markdown") {
		PrintUsage(osStderr, "usage: gregale routes impact [<slug>] --base REF [--head REF] [--path DIR] [--entrypoint MODULE:VARIABLE] [--format text|markdown] [--out PATH] [--fail-on-impact] [--fail-on-incomplete]", "cli")
		return 1
	}
	app := ""
	if len(positional) == 1 {
		app = positional[0]
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return routeImpactError("Invalid --out", errors.New("choose a new file path; existing files and symlinks are not replaced"))
		}
	}
	report, err := routeimpact.Analyze(context.Background(), routeimpact.Options{
		Path: *path, Base: *base, Head: *head, Entrypoint: *entrypoint, App: app,
	})
	if err != nil {
		return routeImpactError("Could not analyze route impact", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return routeImpactError("Could not encode route impact", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return routeImpactError("Could not save route impact", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderRouteImpact(osStdout, report, *format)
		if *output != "" {
			_, _ = fmt.Fprintf(osStdout, "\nSaved JSON report: %s\n", previewReportText(*output))
		}
	}
	summary := report.Summary
	if (*failIncomplete && report.Status != "complete") || (*failImpact && summary.Added+summary.Removed+summary.SourceChanged+summary.PotentiallyAffected > 0) {
		return 1
	}
	return 0
}

// Local subprocess/file errors must not be rendered as API transport failures
// merely because their wrapped error implements net.Error.
func routeImpactError(title string, err error) int {
	return printErr(title, &APIError{Problem: api.Problem{
		Status: 400, Code: "invalid_request", Title: title, Detail: previewReportText(err.Error()),
	}})
}

func renderRouteImpact(w io.Writer, report routeimpact.Report, format string) {
	clean := previewReportText
	if format == "markdown" {
		clean = func(value string) string {
			return strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "`", "\\`", "|", "\\|", "#", "\\#").Replace(previewReportText(value))
		}
	}
	title := "Route impact"
	if report.App != "" {
		title += " for " + clean(report.App)
	}
	if format == "markdown" {
		title = "## " + title
	}
	_, _ = fmt.Fprintf(w, "%s\n\nStatus: %s; framework: %s; source root: %s\nBase: %s; candidate: %s\n",
		title, report.Status, report.Framework, clean(report.SourceRoot), clean(report.Base.Revision), clean(report.Candidate.Revision))
	s := report.Summary
	_, _ = fmt.Fprintf(w, "Added: %d; removed: %d; source changed: %d; potentially affected: %d; no linked changes: %d; unknown: %d\n",
		s.Added, s.Removed, s.SourceChanged, s.PotentiallyAffected, s.NoLinkedChanges, s.Unknown)
	for _, result := range report.Routes {
		prefix := ""
		if format == "markdown" {
			prefix = "### "
		}
		_, _ = fmt.Fprintf(w, "\n%s%s %s — %s\n\n", prefix, clean(result.Method), clean(result.Path), clean(result.Change))
		route := result.After
		if route == nil {
			route = result.Before
		}
		if route != nil {
			_, _ = fmt.Fprintf(w, "  Handler: %s (%s:%d); registered at %s:%d\n", clean(route.Handler), clean(route.Source.File), route.Source.Line, clean(route.Registration.File), route.Registration.Line)
		}
		_, _ = fmt.Fprintf(w, "  Precision: %s\n", clean(result.Precision))
		for _, evidence := range result.Evidence {
			chain := make([]string, len(evidence.Via))
			for i, file := range evidence.Via {
				chain[i] = clean(file)
			}
			for _, location := range evidence.ViaSymbols {
				chain = append(chain, fmt.Sprintf("%s (%s:%d)", clean(location.Name), clean(location.File), location.Line))
			}
			prefix := "  "
			if format == "markdown" {
				prefix = "\n- "
			}
			_, _ = fmt.Fprintf(w, "%s%s %s [%s; %s]: %s\n", prefix, clean(evidence.Change), clean(evidence.File), clean(evidence.Revision), clean(evidence.Kind), strings.Join(chain, " -> "))
		}
		for _, issue := range result.Uncertainties {
			label := issue.Symbol
			if label == "" {
				label = issue.Code
			}
			_, _ = fmt.Fprintf(w, "  Unknown [%s] %s (%s:%d): %s\n", clean(issue.Revision), clean(label), clean(issue.File), issue.Line, clean(issue.Message))
		}
	}
	for _, issue := range report.Issues {
		_, _ = fmt.Fprintf(w, "\nUnknown [%s] %s %s:%d: %s\n", clean(issue.Revision), clean(issue.Code), clean(issue.File), issue.Line, clean(issue.Message))
	}
	_, _ = fmt.Fprintf(w, "\n%s\n", report.Scope)
}
