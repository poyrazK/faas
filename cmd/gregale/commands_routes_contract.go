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
	"github.com/onebox-faas/faas/pkg/routecontract"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

func cmdRoutesContract(args []string) int {
	if len(args) == 0 || args[0] != "check" {
		PrintUsage(osStderr, "usage: gregale routes contract check [<slug>] --openapi FILE --base REF [--head REF] [--path DIR] [--framework auto|fastapi|go-nethttp|node-http] [--entrypoint MODULE:VARIABLE] [--format text|markdown] [--out PATH] [--fail-on-drift] [--fail-on-incomplete] [--json]", "cli")
		return 1
	}
	return cmdRoutesContractCheck(args[1:])
}

func cmdRoutesContractCheck(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-drift", "fail-on-incomplete")
	fs := newFlagSet("routes contract check", flag.ContinueOnError)
	openapi := fs.String("openapi", "", "local OpenAPI 3.0 or 3.1 document")
	base := fs.String("base", "", "baseline Git revision (required)")
	head := fs.String("head", "", "candidate Git revision (defaults to working tree)")
	path := fs.String("path", ".", "application source directory inside a Git repository")
	framework := fs.String("framework", "auto", "source framework: auto, fastapi, go-nethttp, or node-http")
	entrypoint := fs.String("entrypoint", "", "FastAPI module:variable (inferred when exactly one exists)")
	format := fs.String("format", "text", "text or markdown")
	output := fs.String("out", "", "save the JSON report to a new file")
	failDrift := fs.Bool("fail-on-drift", false, "exit 1 for confirmed source-only or contract-only routes")
	failIncomplete := fs.Bool("fail-on-incomplete", false, "exit 1 when route or OpenAPI analysis is inconclusive")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) > 1 || (len(positional) == 1 && !validCLISlug(positional[0])) ||
		*openapi == "" || *base == "" || (*format != "text" && *format != "markdown") ||
		(*framework != "auto" && *framework != "fastapi" && *framework != "go-nethttp" && *framework != "node-http") || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes contract check [<slug>] --openapi FILE --base REF [--head REF] [--path DIR] [--framework auto|fastapi|go-nethttp|node-http] [--entrypoint MODULE:VARIABLE] [--format text|markdown] [--out PATH] [--fail-on-drift] [--fail-on-incomplete] [--json]", "cli")
		return 1
	}
	if *entrypoint != "" && *framework != "auto" && *framework != "fastapi" {
		return routeImpactError("Invalid --entrypoint", errors.New("--entrypoint is only supported with --framework fastapi"))
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return routeImpactError("Invalid --out", errors.New("choose a new file path; existing files and symlinks are not replaced"))
		}
	}
	file, err := openCustomerFile(*openapi)
	if err != nil {
		return routeImpactError("Invalid --openapi", errors.New("use a readable regular file without symlinks"))
	}
	document, readErr := io.ReadAll(io.LimitReader(file, api.RouteImpactSourceMaxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(document)) > api.RouteImpactSourceMaxBytes {
		return routeImpactError("Invalid --openapi", errors.New("OpenAPI document could not be read or exceeds the 16 MiB limit"))
	}

	app := ""
	if len(positional) == 1 {
		app = positional[0]
	}
	source, err := routeimpact.Analyze(context.Background(), routeimpact.Options{
		Path: *path, Base: *base, Head: *head, Framework: *framework, Entrypoint: *entrypoint, App: app,
	})
	if err != nil {
		return routeImpactError("Could not analyze route source", err)
	}
	report, err := routecontract.Compare(document, *openapi, source)
	if err != nil {
		return routeImpactError("Could not check route contract", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return routeImpactError("Could not encode route contract", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return routeImpactError("Could not save route contract", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderRouteContract(osStdout, report, *format)
		if *output != "" {
			_, _ = fmt.Fprintf(osStdout, "\nSaved JSON report: %s\n", previewReportText(*output))
		}
	}
	if (*failDrift && report.Outcome == "drift") || (*failIncomplete && report.Outcome == "incomplete") {
		return 1
	}
	return 0
}

func renderRouteContract(w io.Writer, report routecontract.Report, format string) {
	clean := previewReportText
	if format == "markdown" {
		clean = func(value string) string {
			return strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "`", "\\`", "|", "\\|", "#", "\\#").Replace(previewReportText(value))
		}
	}
	title := "Route contract"
	if report.App != "" {
		title += " for " + clean(report.App)
	}
	if format == "markdown" {
		title = "## " + title
	}
	_, _ = fmt.Fprintf(w, "%s\n\nOutcome: %s; analysis: %s; framework: %s\nOpenAPI: %s (version %s; SHA-256 %s)\nSource: %s; base: %s; candidate: %s\n",
		title, report.Outcome, report.Status, clean(report.Framework), clean(report.OpenAPIFile), clean(report.OpenAPIVersion), clean(report.OpenAPISHA256),
		clean(report.SourceRoot), clean(report.BaseRevision), clean(report.Candidate))
	s := report.Summary
	_, _ = fmt.Fprintf(w, "Contract routes: %d; source routes: %d; matched: %d; source-only: %d; contract-only: %d; unknown: %d\n",
		s.ContractRoutes, s.SourceRoutes, s.Matched, s.SourceOnly, s.ContractOnly, s.Unknown)
	for _, route := range report.Routes {
		label := route.ContractPath
		if label == "" {
			label = route.SourcePath
		}
		if route.SourcePath != "" && route.ContractPath != "" && route.SourcePath != route.ContractPath {
			label += " => " + route.SourcePath
		}
		prefix := ""
		if format == "markdown" {
			prefix = "- "
		}
		_, _ = fmt.Fprintf(w, "\n%s%s %s — %s", prefix, clean(route.Method), clean(label), clean(route.Status))
		if route.Match != "" {
			_, _ = fmt.Fprintf(w, " (%s match)", clean(route.Match))
		}
		_, _ = fmt.Fprintln(w)
		if route.Handler != "" {
			_, _ = fmt.Fprintf(w, "  Handler: %s\n", clean(route.Handler))
		}
		if route.Source != nil {
			_, _ = fmt.Fprintf(w, "  Source: %s:%d\n", clean(route.Source.File), route.Source.Line)
		}
		if route.Registration != nil {
			_, _ = fmt.Fprintf(w, "  Registered: %s:%d\n", clean(route.Registration.File), route.Registration.Line)
		}
		for _, candidate := range route.Candidates {
			_, _ = fmt.Fprintf(w, "  Candidate: %s (%s)", clean(candidate.Path), clean(candidate.Handler))
			if candidate.Registration != nil {
				_, _ = fmt.Fprintf(w, " at %s:%d", clean(candidate.Registration.File), candidate.Registration.Line)
			}
			_, _ = fmt.Fprintln(w)
		}
		if route.CandidatesOmitted > 0 {
			_, _ = fmt.Fprintf(w, "  Additional candidates omitted: %d\n", route.CandidatesOmitted)
		}
		if route.Reason != "" {
			_, _ = fmt.Fprintf(w, "  %s\n", clean(route.Reason))
		}
	}
	for _, issue := range report.ContractIssues {
		_, _ = fmt.Fprintf(w, "\nUnknown OpenAPI [%s] %s: %s\n", clean(issue.Code), clean(issue.Path), clean(issue.Message))
	}
	if report.ContractIssuesOmitted > 0 {
		_, _ = fmt.Fprintf(w, "\nAdditional OpenAPI issues omitted: %d\n", report.ContractIssuesOmitted)
	}
	for _, issue := range report.SourceIssues {
		_, _ = fmt.Fprintf(w, "\nUnknown source [%s] %s:%d: %s\n", clean(issue.Code), clean(issue.File), issue.Line, clean(issue.Message))
	}
	_, _ = fmt.Fprintln(w, "\nStatic analysis does not execute application code; review dynamic registrations before relying on route absence.")
}
