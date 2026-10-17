package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func cmdSitesDiagnose(args []string) int {
	enabled := os.Getenv("FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED")
	if enabled != "true" && enabled != "1" {
		return printErr("Hosting diagnostics are disabled", errors.New("set FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED=1; API opt-in is separate"))
	}
	f := newFlagSet("sites-diagnose", flag.ContinueOnError)
	site := f.String("site", "", "owned site UUID")
	run := f.String("run-id", "", "optional original CI run UUID")
	dir := f.String("path", "", "optional prebuilt directory to compare without uploading")
	spa := f.Bool("spa", false, "use the original SPA setting when comparing output")
	trust := f.String("oidc-trust", "", "optional explicit GitHub site trust for job identity")
	if err := f.Parse(args); err != nil || rejectUnexpectedFlagArgs(f) {
		return 1
	}
	if !hosting.ValidCIRunID(*site) || *run != "" && !hosting.ValidCIRunID(*run) || *dir != "" && *run == "" || *spa && *dir == "" {
		return printErr("Invalid diagnostic request", errors.New("provide a canonical site UUID and optional original run UUID; --path needs --run-id and --spa needs --path"))
	}
	var local *api.HostingManifest
	if *dir != "" {
		manifest, err := staticHostingManifest(*dir, *spa)
		if err != nil {
			return printErr("Cannot compare prebuilt output", err)
		}
		local = &manifest
	}
	client, err := hostingDiagnosticClient(*site, *trust)
	if err != nil {
		return printErr("Diagnostic connection unavailable", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.HostingDiagnosticsTimeout)
	defer cancel()
	caps, err := client.GetHostingCapabilities(ctx)
	if err != nil {
		return printErr("Capabilities unavailable", err)
	}
	if !caps.DiagnosticsEnabled {
		return printErr("API diagnostics are disabled", errors.New("ask an operator to review the independent diagnostics flag"))
	}
	report, err := client.GetHostingDiagnostics(ctx, *site, *run)
	if err != nil {
		return printErr("Diagnostics unavailable", err)
	}
	if report.SiteID != *site || report.RunID != *run || report.ServingHealth != "not_checked" {
		return printErr("Invalid diagnostic response", errors.New("site, run or serving evidence does not match the request"))
	}
	if local != nil {
		compareDiagnosticManifest(&report, *local)
	}
	// Success means the read-only report was produced, not that hosting is healthy
	// or that a retry is guaranteed. Errors never become a partial healthy report.
	return jsonOut(writeJSON(report))
}

func hostingDiagnosticClient(site, trust string) (*api.Client, error) {
	if trust != "" {
		enabled := os.Getenv("FAAS_FRONTEND_HOSTING_OIDC_ENABLED")
		if !hostingAutomationWorkstationEnabled() || enabled != "1" && enabled != "true" || os.Getenv("FAAS_TOKEN") != "" {
			return nil, errors.New("enable automation/OIDC workstation flags and omit FAAS_TOKEN for GitHub identity")
		}
		return api.NewGitHubHostingOIDCClient(apiBase(), site, trust, os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
	}
	token := loadToken()
	base := apiBase()
	u, err := url.Parse(base)
	loopback := u != nil && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())
	if err != nil || u == nil || token == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return nil, errors.New("use an existing operator/site credential and an HTTPS API origin; loopback HTTP is allowed for local tests")
	}
	return api.NewHostingAutomationClient(strings.TrimSuffix(base, "/"), token), nil
}

func compareDiagnosticManifest(report *api.HostingDiagnostics, local api.HostingManifest) {
	check := api.HostingDiagnosticCheck{Step: "local_output", State: "unknown", Code: "manifest_unavailable", Summary: "No retained manifest is available for comparison.", Action: "Inspect the original CI artifacts and retained resources before retrying."}
	switch {
	case report.ManifestFingerprint == "":
		report.RetryAdvice = "inspect_before_retry"
	case hosting.HostingManifestFingerprint(local) != report.ManifestFingerprint:
		check.State, check.Code, check.Summary, check.Action = "blocked", "manifest_changed", "Local prebuilt output differs from the retained release manifest.", "Restore the original output and SPA/config settings. Do not reuse the run ID for changed files or hide an uncertain failure with a new ID."
		report.RetryAdvice = "inspect_before_retry"
	default:
		check.State, check.Code, check.Summary, check.Action = "ok", "manifest_matches", "Local output matches the retained manifest.", "Source labels and preview TTL must also remain unchanged; they were not compared. The server still revalidates any retry."
	}
	report.Checks = append(report.Checks, check)
}
