package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// cmdMCPReplay is `gregale mcp server replay` (ADR-20261122194324): compare a
// zero-traffic candidate with the serving deployment before promotion. It
// sends only discovery, static resource reads and reviewed fixtures, and
// writes nothing.
func cmdMCPReplay(args []string) int {
	fs := newFlagSet("mcp-replay", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	id := fs.String("candidate", "", "candidate deployment UUID with zero traffic")
	path := fs.String("path", ".", "MCP source directory")
	tokenEnv := fs.String("token-env", "", "environment variable holding a client credential for both deployments")
	fixtures := fs.String("fixtures", "", "reviewed fixture JSON for prompts and allowed tool calls")
	runs := fs.Int("runs", 3, "times to send each probe to each deployment (1-5)")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall deadline")
	var allow multiFlag
	fs.Var(&allow, "allow-tool", "tool whose fixtures may be called on both deployments; repeatable")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *slug == "" || *id == "" {
		return printErr("MCP replay", errors.New("--app and --candidate are required"))
	}
	if *runs < 1 || *runs > mcphosting.ReplayMaxRuns {
		return printErr("MCP replay", fmt.Errorf("--runs must be between 1 and %d", mcphosting.ReplayMaxRuns))
	}
	dir, err := filepath.Abs(*path)
	if err != nil {
		return printErr("MCP source", err)
	}
	cfg, err := mcphosting.Load(dir)
	if err != nil {
		return printErr("MCP configuration", err)
	}
	options := mcphosting.ReplayOptions{Runs: *runs, AllowTools: allow}
	if *fixtures != "" {
		f, err := openCustomerFile(*fixtures)
		if err != nil {
			return printErr("MCP fixtures", err)
		}
		suite, err := mcphosting.LoadSmokeSuite(f)
		_ = f.Close()
		if err != nil {
			return printErr("MCP fixtures", err)
		}
		options.Fixtures = &suite
	} else if len(allow) > 0 {
		return printErr("MCP replay", errors.New("--allow-tool needs --fixtures: tools are called only with reviewed arguments"))
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil {
		return printErr("MCP client token", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	servingURL, candidateURL, err := mcpReplayTargets(ctx, c, *slug, *id, cfg)
	if err != nil {
		return printErr("MCP replay", err)
	}
	serving, err := mcphosting.NewClient(servingURL, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	candidate, err := mcphosting.NewClient(candidateURL, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	report := mcphosting.Replay(ctx, serving, candidate, options)
	if jsonOutput {
		_ = writeJSONStdout(report)
	} else {
		printMCPReplay(report)
	}
	if !report.OK {
		return 1
	}
	return 0
}

// mcpReplayTargets resolves the serving endpoint and the candidate's preview
// endpoint without changing the app.
func mcpReplayTargets(ctx context.Context, c *Client, slug, id string, cfg mcphosting.Config) (string, string, error) {
	dep, err := c.GetDeployment(ctx, id)
	if err != nil {
		return "", "", err
	}
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		return "", "", err
	}
	if dep.AppID != app.ID || dep.Status != statusLive || dep.TrafficPercent != 0 {
		return "", "", errors.New("the candidate must be a live deployment of the app with zero traffic (deploy it with --no-traffic)")
	}
	serving, err := mcpServingDeployment(ctx, c, slug, id)
	if err != nil {
		return "", "", err
	}
	if serving == "" {
		return "", "", errors.New("the app has no serving deployment to compare with")
	}
	endpoint, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return "", "", err
	}
	candidate, err := mcpCandidatePreview(ctx, c, id, cfg)
	return endpoint, candidate, err
}

func printMCPReplay(r mcphosting.ReplayReport) {
	for _, problem := range r.Problems {
		PrintWarn(osStderr, "%s", problem)
	}
	switch {
	case r.Catalog.Breaking:
		PrintWarn(osStdout, "Catalog: breaking changes")
	case r.Catalog.NeedsReview:
		PrintWarn(osStdout, "Catalog: changes need review")
	default:
		PrintOK(osStdout, "Catalog: compatible")
	}
	for _, change := range r.Catalog.Changes {
		_, _ = fmt.Fprintf(osStdout, "  %-12s %s %s %s\n", change.Severity, change.Tool, change.Path, change.Kind)
	}
	for _, p := range r.Probes {
		_, _ = fmt.Fprintf(osStdout, "  %-11s %-8s %s  serving %d ms (%d errors)  candidate %d ms (%d errors)\n",
			p.Outcome, p.Kind, p.Target, p.Serving.MedianMS, p.Serving.Errors, p.Candidate.MedianMS, p.Candidate.Errors)
	}
	for _, skipped := range r.Skipped {
		_, _ = fmt.Fprintf(osStdout, "  skipped: %s\n", skipped)
	}
	if r.OK {
		PrintOK(osStdout, "Candidate replay passed")
	} else {
		PrintWarn(osStdout, "Candidate replay failed: a breaking catalog change, a new error, or an unreadable deployment")
	}
}

// withMCPReplayGate adds the deploy-time replay gate (ADR-20261123110137) to
// smoke, creating a fixture-less gate when none was given.
func withMCPReplayGate(smoke *mcpSmokeGate, replay bool) (*mcpSmokeGate, error) {
	if !replay {
		return smoke, nil
	}
	if !mcpGatewayPreviewCLI() {
		return nil, errors.New("--replay is a preview; set FAAS_MCP_GATEWAY_PREVIEW_ENABLED=1")
	}
	if smoke == nil {
		smoke = &mcpSmokeGate{}
	}
	smoke.replay = true
	return smoke, nil
}

// mcpReplayGateReport is the replay section of a deploy receipt.
type mcpReplayGateReport struct {
	Skipped string                   `json:"skipped,omitempty"`
	Report  *mcphosting.ReplayReport `json:"report,omitempty"`
}

// runMCPReplayGate replays the candidate against the serving endpoint with
// the gate's prompt fixtures. It never calls a tool: --allow-tool-execution
// consents to one call on the candidate, not to calls on the serving
// production deployment, so tool fixtures are reported as skipped (manual
// `mcp server replay --allow-tool` names production tools explicitly). A
// first deployment has nothing to compare with and passes. It compares the
// catalog the --token-env credential sees; release-policy roles keep their
// own catalog checks in verifyMCPRoles.
func runMCPReplayGate(ctx context.Context, endpoint, candidate, serving, token string, gate *mcpSmokeGate) (mcpReplayGateReport, bool) {
	if serving == "" {
		return mcpReplayGateReport{Skipped: "no serving deployment to compare with"}, true
	}
	options := mcphosting.ReplayOptions{}
	if len(gate.suite.Cases) > 0 {
		options.Fixtures = &gate.suite
	}
	// Its own budget: earlier gates may have spent most of the verification
	// deadline, and a deadline hit between the serving and candidate calls
	// would read as a new error on a good candidate.
	replayCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	servingClient, err := mcphosting.NewClient(endpoint, token, mcphosting.ProtocolVersion)
	if err != nil {
		return mcpReplayGateReport{Report: &mcphosting.ReplayReport{Problems: []string{"serving endpoint: " + err.Error()}}}, false
	}
	candidateClient, err := mcphosting.NewClient(candidate, token, mcphosting.ProtocolVersion)
	if err != nil {
		return mcpReplayGateReport{Report: &mcphosting.ReplayReport{Problems: []string{"candidate endpoint: " + err.Error()}}}, false
	}
	report := mcphosting.Replay(replayCtx, servingClient, candidateClient, options)
	return mcpReplayGateReport{Report: &report}, report.OK
}

// mcpReplayGateError is the receipt's promotion_error for a failed gate.
func mcpReplayGateError(r mcpReplayGateReport) string {
	if r.Report != nil && len(r.Report.Problems) > 0 {
		return "MCP replay could not read a deployment (" + r.Report.Problems[0] + "); candidate remains unpromoted"
	}
	return "MCP replay found a breaking catalog change or a new error; candidate remains unpromoted"
}
