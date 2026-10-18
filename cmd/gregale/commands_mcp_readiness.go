package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpDeploymentReadiness struct {
	App          string                     `json:"app"`
	DeploymentID string                     `json:"deployment_id"`
	Endpoint     string                     `json:"endpoint"`
	Readiness    mcphosting.ReadinessReport `json:"readiness"`
}

func cmdMCPReadiness(args []string) int {
	if len(args) > 0 && args[0] == "pagerduty" {
		return cmdMCPReadinessPagerDuty(args[1:])
	}
	if len(args) > 0 && args[0] == "reminder-defaults" {
		return cmdMCPReadinessReminderDefaults(args[1:])
	}
	if len(args) > 0 && args[0] == "fallback" {
		return cmdMCPReadinessFallback(args[1:])
	}
	if len(args) > 0 && args[0] == "attention" {
		return cmdMCPReadinessAttention(args[1:])
	}
	if len(args) > 0 && args[0] == "notifications" {
		return cmdMCPReadinessNotifications(args[1:])
	}
	if len(args) > 0 && args[0] == "maintenance" {
		return cmdMCPReadinessMaintenance(args[1:])
	}
	if len(args) > 0 && args[0] == "incidents" {
		return cmdMCPReadinessIncidents(args[1:])
	}
	if len(args) > 0 && args[0] == "policy" {
		return cmdMCPReadinessPolicy(args[1:])
	}
	fs := newFlagSet("mcp-readiness", flag.ContinueOnError)
	app := fs.String("app", "", "app slug owning the deployment")
	deployment := fs.String("deployment", "", "deployment UUID to probe through its public preview endpoint")
	endpoint := fs.String("endpoint", "/mcp", "MCP endpoint path")
	tokenEnv := fs.String("token-env", "", "environment variable containing a dedicated, scoped MCP probe token")
	baselinePath := fs.String("baseline", "", "optional reviewed MCP catalog lock file")
	record := fs.Bool("record", false, "run a platform probe and persist policy-bound evidence")
	role := fs.String("role", "default", "configured probe role with --record")
	legacy := fs.Bool("legacy", false, "check the legacy protocol")
	timeout := fs.Duration("timeout", 30*time.Second, "overall deadline, at most 30s")
	if fs.Parse(args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *app == "" || *deployment == "" || *timeout <= 0 || *timeout > 30*time.Second {
		return printErr("MCP readiness", errors.New("--app, --deployment and a timeout in (0,30s] are required"))
	}
	if _, e := uuid.Parse(*deployment); e != nil {
		return printErr("MCP readiness", errors.New("--deployment must be a UUID"))
	}
	if *record && (*baselinePath != "" || *legacy || *endpoint != "/mcp") {
		return printErr("MCP readiness", errors.New("--record uses the platform policy; omit --baseline, --legacy and --endpoint"))
	}
	version := mcphosting.ProtocolVersion
	if *legacy {
		version = mcphosting.LegacyProtocolVersion
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil {
		return printErr("MCP readiness", err)
	}
	var baseline *mcphosting.Contract
	if *baselinePath != "" {
		f, e := openCustomerFile(*baselinePath)
		if e != nil {
			return printErr("MCP readiness baseline", e)
		}
		contract, e := mcphosting.ReadContract(f)
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return printErr("MCP readiness baseline", e)
		}
		if contract.ProtocolVersion != version {
			return printErr("MCP readiness baseline", errors.New("baseline protocol does not match requested protocol"))
		}
		baseline = &contract
	}
	platform, err := authedClient()
	if err != nil {
		return printErr("MCP readiness", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	metadata, err := platform.GetApp(ctx, *app)
	if err != nil {
		return printErr("MCP readiness", err)
	}
	dep, err := platform.GetDeployment(ctx, *deployment)
	if err != nil {
		return printErr("MCP readiness", err)
	}
	requestedID, _ := uuid.Parse(*deployment)
	returnedID, idErr := uuid.Parse(dep.ID)
	if dep.AppID != metadata.ID || idErr != nil || returnedID != requestedID || dep.Status != statusLive {
		return printErr("MCP readiness", errors.New("deployment must be live and belong to the selected app"))
	}
	if *record {
		evidence, e := platform.ProbeMCPReadiness(ctx, *deployment, *role, token)
		if e != nil {
			return printErr("MCP platform readiness", e)
		}
		if code := writeJSONStdout(evidence); code != 0 {
			return code
		}
		if !evidence.Passed {
			return 1
		}
		return 0
	}
	url, err := mcpCandidatePreview(ctx, platform, *deployment, mcphosting.Config{Endpoint: *endpoint})
	if err != nil {
		return printErr("MCP readiness", err)
	}
	probe, err := mcphosting.NewClient(url, token, version)
	if err != nil {
		return printErr("MCP readiness", err)
	}
	report := mcpDeploymentReadiness{App: *app, DeploymentID: *deployment, Endpoint: url, Readiness: mcphosting.CheckReadiness(ctx, probe, baseline)}
	if jsonOutput {
		if code := writeJSONStdout(report); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "MCP readiness for %s deployment %s\n", *app, *deployment)
		for _, check := range report.Readiness.Checks {
			_, _ = fmt.Fprintf(osStdout, "%s: %s %s\n", check.Name, check.Status, check.Detail)
		}
	}
	if !report.Readiness.OK {
		return 1
	}
	return 0
}
