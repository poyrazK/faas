package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func checkMCPNativeRetirement(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState) error {
	if s.Stage != "web_restored" && s.Stage != "quarantined" && s.Stage != "retirement_pending" && s.Stage != "worker_retired" {
		return errors.New("restore web traffic before quarantining candidate workers")
	}
	if err := requireMCPConditionalParking(ctx, c); err != nil {
		return err
	}
	current, err := mcpServingDeployment(ctx, c, p.WebApp, "")
	if err != nil {
		return err
	}
	if current != s.ServingDeployment || current == "" {
		return errors.New("restored serving revision changed")
	}
	if err := mcpNativeObserverHealthy(ctx, c, p); err != nil {
		return err
	}
	if err := checkMCPNativePrevious(ctx, c, p, s); err != nil {
		return err
	}
	previous := *s
	previous.WebDeployment = s.ServingDeployment
	previous.Promoted = true
	if err := mcpNativeVerifyEndpoint(ctx, c, p, &previous); err != nil {
		return err
	}
	dep, err := c.GetLatestAppDeployment(ctx, p.WorkerApp)
	if err != nil {
		return err
	}
	if dep.ID != s.WorkerDeployment || s.WorkerDeployment == "" {
		return errors.New("candidate generation changed; refusing app-wide retirement")
	}
	instances, err := c.ListInstances(ctx, p.WorkerApp)
	if err != nil {
		return err
	}
	if len(instances) == 0 && (s.Stage == "retirement_pending" || s.Stage == "worker_retired") {
		return nil
	}
	for _, instance := range instances {
		if instance.DeploymentID != s.WorkerDeployment {
			return errors.New("candidate app contains another worker generation")
		}
	}
	ids, err := mcpNativeWorkerIDs(ctx, c, p.WorkerApp, s.WorkerDeployment, true)
	if err != nil {
		return err
	}
	if strings.Join(ids, ",") != strings.Join(s.WorkerIDs, ",") {
		return errors.New("candidate worker instances changed; do not substitute another generation")
	}
	return nil
}

func retireMCPNativeWorker(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState, state string) error {
	if err := checkMCPNativeRetirement(ctx, c, p, s); err != nil {
		return err
	}
	app, err := c.GetApp(ctx, p.WorkerApp)
	if err != nil {
		return err
	}
	if !isWorkerApp(app) {
		return errors.New("candidate retirement requires a worker app")
	}
	cfg, err := mcphosting.Load(p.WorkerPath)
	if err != nil {
		return err
	}
	deadline := 30 * time.Second
	if cfg.Tasks != nil && cfg.Tasks.ShutdownTimeoutMS != 0 {
		deadline = time.Duration(cfg.Tasks.ShutdownTimeoutMS) * time.Millisecond
	}
	if app.Manifest.StopGracePeriod <= deadline {
		return errors.New("candidate stop grace must exceed the Task shutdown deadline")
	}
	s.Stage = "retirement_pending"
	if err := saveMCPNativeState(state, s); err != nil {
		return err
	}
	if err := c.ParkIfDeployment(ctx, p.WorkerApp, s.WorkerDeployment); err != nil {
		return err
	}
	return nil
}

func runMCPNativeRetirement(p mcpNativeReleasePlan, s mcpNativeReleaseState, state, plan string, retire bool) int {
	if !retire && (s.Stage == "retirement_pending" || s.Stage == "worker_retired") {
		return printErr("MCP retirement", errors.New("retirement already started; resume retire instead of quarantining again"))
	}
	if s.PendingSubmission != "" || len(s.WorkerIDs) == 0 {
		return printErr("MCP retirement", errors.New("candidate submission and worker IDs must be known"))
	}
	if err := preflightMCPControlPlane(); err != nil {
		return printErr("MCP control-plane compatibility", err)
	}
	binary, err := os.Executable()
	if err != nil {
		return printErr("MCP retirement", err)
	}
	absolutePlan, err := filepath.Abs(plan)
	if err != nil {
		return printErr("MCP retirement", err)
	}
	check := []string{binary, "--json", "mcp", "tasks", "release", "retire-check", "--plan", absolutePlan, "--state", state}
	park := []string{binary, "--json", "mcp", "tasks", "release", "retire-hook", "--plan", absolutePlan, "--state", state}
	input, _ := json.Marshal(map[string]any{"check": check, "park": park, "workerIDs": s.WorkerIDs, "retire": retire, "timeoutMs": p.TimeoutSeconds * 1000})
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.TimeoutSeconds*4+120)*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "tasks-release-retire.js", string(input))
	command.Dir = p.WorkerPath
	output, err := command.Output()
	var report struct {
		OK    bool   `json:"ok"`
		Stage string `json:"stage"`
	}
	if json.Unmarshal(output, &report) != nil {
		return printErr("MCP retirement", errors.New("retirement gate unavailable; check updated Node worker sources and Task bindings"))
	}
	if report.OK {
		s, err = loadMCPNativeState(state, s.Fingerprint)
		if err != nil {
			return printErr("MCP retirement journal", err)
		}
		if !retire && (s.Stage == "retirement_pending" || s.Stage == "worker_retired") {
			return printErr("MCP retirement journal", errors.New("retirement started concurrently; do not downgrade its journal"))
		}
		if retire {
			s.Stage = "worker_retired"
		} else {
			s.Stage = "quarantined"
		}
		if err := saveMCPNativeState(state, &s); err != nil {
			return printErr("MCP retirement journal", err)
		}
	}
	if code := jsonOut(writeJSON(report)); code != 0 {
		return code
	}
	if err != nil || !report.OK {
		return 1
	}
	return 0
}
