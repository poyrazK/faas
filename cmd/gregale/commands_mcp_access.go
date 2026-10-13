package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpAccessAuditOptions struct {
	AuthPlacement string   `json:"auth_placement"`
	Algorithms    []string `json:"algorithms,omitempty"`
}

type mcpAccessAuditReport struct {
	Version           int                             `json:"version"`
	App               string                          `json:"app"`
	ObservedAt        time.Time                       `json:"observed_at"`
	AuthPlacement     string                          `json:"auth_placement"`
	OK                bool                            `json:"ok"`
	RuntimeVerified   bool                            `json:"runtime_verified"`
	RolesRequested    bool                            `json:"roles_requested"`
	PolicyExpectation *api.MCPAccessPolicyExpectation `json:"policy_expectation,omitempty"`
	RuleID            string                          `json:"rule_id,omitempty"`
	Checks            []mcphosting.Check              `json:"checks"`
	Actions           []mcpOverviewAction             `json:"actions"`
}

func newMCPAccessAuditOptions(placement, algorithms string) (*mcpAccessAuditOptions, error) {
	if placement == "" && algorithms == "" {
		return nil, nil
	}
	options := &mcpAccessAuditOptions{AuthPlacement: placement}
	if algorithms != "" {
		options.Algorithms = strings.Split(algorithms, ",")
	}
	return options, normalizeMCPAccessAuditOptions(options)
}

func normalizeMCPAccessAuditOptions(options *mcpAccessAuditOptions) error {
	if options == nil {
		return nil
	}
	if options.AuthPlacement != "gateway" && options.AuthPlacement != "guest-only" {
		return errors.New("auth placement must be gateway or guest-only")
	}
	if options.AuthPlacement == "guest-only" {
		if len(options.Algorithms) != 0 {
			return errors.New("gateway algorithms are not applicable to guest-only auditing")
		}
		return nil
	}
	if len(options.Algorithms) == 0 {
		options.Algorithms = []string{"RS256", "ES256"}
	}
	seen := map[string]bool{}
	for _, alg := range options.Algorithms {
		if (alg != "RS256" && alg != "ES256") || seen[alg] {
			return errors.New("reviewed gateway algorithms must be a unique subset of RS256,ES256")
		}
		seen[alg] = true
	}
	return nil
}

func cmdMCPAccess(args []string) int {
	if len(args) > 0 && args[0] == "explain" {
		return cmdMCPAccessExplain(args[1:])
	}
	if len(args) > 0 && args[0] == "history" {
		return cmdMCPAccessHistory(args[1:])
	}
	if len(args) == 0 || args[0] != "audit" {
		return printErr("MCP access", errors.New("use mcp access audit, history or explain"))
	}
	fs := newFlagSet("mcp-access-audit", flag.ContinueOnError)
	app := fs.String("app", "", "MCP application slug")
	path := fs.String("path", ".", "reviewed MCP source directory")
	placement := fs.String("auth-placement", "", "gateway or guest-only (required)")
	algorithms := fs.String("algorithms", "", "reviewed gateway algorithms (default RS256,ES256)")
	policy := fs.String("release-policy", "", "optional role baselines and authorization exclusions")
	timeout := fs.Duration("timeout", 30*time.Second, "overall audit deadline, at most 2m")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	options, err := newMCPAccessAuditOptions(*placement, *algorithms)
	if err != nil || options == nil || fs.NArg() != 0 || !api.ValidAppSlug(*app) || *timeout <= 0 || *timeout > 2*time.Minute {
		return printErr("Invalid MCP access audit", errors.New("supply a valid --app, explicit --auth-placement and positive --timeout up to 2m; gateway algorithms must be RS256 and/or ES256"))
	}
	cfg, err := mcphosting.Load(*path)
	if err != nil {
		return printErr("MCP access source", errors.New("could not load a valid gregale-mcp.json from the reviewed source directory"))
	}
	roles, err := loadMCPReleasePolicyWithTokens(*policy, false)
	if err != nil {
		return printErr("MCP access release policy", errors.New("could not load valid confined role baselines and authorization declarations"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	return writeMCPAccessAudit(collectMCPAccessAudit(ctx, client, *app, cfg, roles, *options))
}

func collectMCPAccessAudit(ctx context.Context, client *Client, slug string, cfg mcphosting.Config, roles []mcpReleaseRole, options mcpAccessAuditOptions) mcpAccessAuditReport {
	report := mcpAccessAuditReport{Version: 1, App: slug, ObservedAt: time.Now().UTC(), AuthPlacement: options.AuthPlacement, RolesRequested: len(roles) > 0, Checks: mcpAccessSourceChecks(cfg, roles), Actions: []mcpOverviewAction{{Label: "Inspect account edge rules", Arguments: []string{"gregale", "edge-rules", "list"}}}}
	if normalizeMCPAccessAuditOptions(&options) != nil {
		report.Checks = append(report.Checks, mcphosting.Check{Name: "audit_options", Status: "failed", Detail: "Invalid reviewed auth placement or algorithms."})
		return report
	}
	app, err := client.GetApp(ctx, slug)
	if err != nil || app.Slug != slug || app.ID == "" {
		report.Checks = append(report.Checks, mcphosting.Check{Name: "application_metadata", Status: "unknown", Detail: "Could not verify the selected app's identity and canonical endpoint."})
		return report
	}
	endpoint, urlErr := cfg.URL(canonicalAppURL(app))
	if urlErr != nil || endpoint != cfg.Auth.Resource {
		report.Checks = append(report.Checks, mcphosting.Check{Name: "canonical_resource", Status: "failed", Detail: "Reviewed OAuth resource must match the selected app's canonical MCP endpoint."})
		return report
	}
	report.Checks = append(report.Checks, mcphosting.Check{Name: "canonical_resource", Status: "passed", Detail: "Reviewed resource matches the app's canonical endpoint."})
	rules, err := client.ListEdgeRules(ctx)
	if err != nil {
		report.Checks = append(report.Checks, mcphosting.Check{Name: "account_gateway_rules", Status: "unknown", Detail: "Could not inspect account-wide gateway rules; check read permissions and connectivity."})
		return report
	}
	report.RuleID, report.Checks = auditMCPGatewayRules(app.ID, cfg, options, rules, report.ObservedAt, report.Checks)
	fingerprint, fingerprintErr := api.MCPAccessPolicyFingerprint(cfg.Auth.Resource, rules, report.ObservedAt)
	if fingerprintErr != nil {
		report.Checks = append(report.Checks, mcphosting.Check{Name: "policy_fingerprint", Status: "unknown", Detail: "Could not fingerprint stored access policy."})
	}
	report.OK = mcpAuthorizationChecksPassed(report.Checks)
	var accountRevision int64
	if report.OK {
		report.Checks = append(report.Checks, waitMCPAccessConvergence(ctx, client, slug, app.ID, cfg.Auth.Resource, fingerprint, &accountRevision))
		report.OK = mcpAuthorizationChecksPassed(report.Checks)
	}
	if report.OK {
		report.PolicyExpectation = &api.MCPAccessPolicyExpectation{Resource: cfg.Auth.Resource, Fingerprint: fingerprint, AccountRevision: &accountRevision}
	}
	return report
}

func writeMCPAccessAudit(report mcpAccessAuditReport) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "MCP access audit for %s: placement=%s, audit passed=%t\n", report.App, report.AuthPlacement, report.OK)
		for _, check := range report.Checks {
			_, _ = fmt.Fprintf(osStdout, "%s: %s — %s\n", check.Name, check.Status, check.Detail)
		}
		_, _ = fmt.Fprintln(osStdout, "Live token enforcement is not verified by this audit.")
	}
	if !report.OK {
		return 1
	}
	return 0
}
