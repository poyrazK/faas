package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func checkMCPNativePrevious(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState) error {
	if !s.ServingCaptured || len(s.PreviousDeployments) != len(p.PreviousWorkerApps) {
		return errors.New("previous generations were not captured")
	}
	for _, slug := range p.PreviousWorkerApps {
		if s.Parked[slug] {
			return errors.New("previous worker was already parked; restore requires running previous workers")
		}
		dep, err := c.GetLatestAppDeployment(ctx, slug)
		if err != nil {
			return err
		}
		if dep.ID != s.PreviousDeployments[slug] {
			return errors.New("previous worker generation changed")
		}
		ids, err := mcpNativeWorkerIDs(ctx, c, slug, dep.ID)
		if err != nil {
			return err
		}
		previous := *s
		previous.WorkerDeployment, previous.WorkerIDs = dep.ID, ids
		previousPlan := p
		previousPlan.WorkerApp = slug
		// Require each previous generation to cover the entire candidate inventory.
		if err := mcpNativeCheckWorkers(ctx, c, previousPlan, &previous); err != nil {
			return err
		}
	}
	return nil
}

type mcpNativeRecoveryCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func recoverMCPNativeRelease(p mcpNativeReleasePlan, s mcpNativeReleaseState, state, plan string, resume bool) int {
	c, err := authedClient()
	if err != nil {
		return printErr("MCP recovery", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	serving, err := mcpServingDeployment(ctx, c, p.WebApp, "")
	if err != nil {
		return printErr("MCP recovery", err)
	}
	changed := !s.ServingCaptured || (serving != s.ServingDeployment && serving != s.WebDeployment)
	check := func(name string, err error) mcpNativeRecoveryCheck {
		if err != nil {
			return mcpNativeRecoveryCheck{Name: name, Status: "failed", Detail: err.Error()}
		}
		return mcpNativeRecoveryCheck{Name: name, Status: "passed", Detail: "Readiness check passed"}
	}
	trafficErr := error(nil)
	if changed {
		trafficErr = fmt.Errorf("serving deployment %q differs from the captured baseline %q and candidate %q", serving, s.ServingDeployment, s.WebDeployment)
	}
	checks := []mcpNativeRecoveryCheck{check("serving_generation", trafficErr)}
	candidateErr := error(nil)
	if s.WorkerDeployment == "" || len(s.WorkerIDs) == 0 {
		candidateErr = errors.New("candidate worker deployment or captured worker IDs are missing")
	} else {
		candidateErr = mcpNativeCheckWorkers(ctx, c, p, &s)
	}
	checks = append(checks, check("candidate_workers", candidateErr))
	observerErr := mcpNativeObserverHealthy(ctx, c, p)
	checks = append(checks, check("observer", observerErr))
	endpointErr := error(nil)
	if changed {
		endpointErr = errors.New("candidate endpoint cannot be verified while the serving generation has changed")
	} else if serving == "" || s.WebDeployment == "" {
		endpointErr = errors.New("captured serving and candidate deployment IDs are required")
	} else {
		endpointErr = mcpNativeVerifyEndpoint(ctx, c, p, &s)
	}
	checks = append(checks, check("candidate_endpoint", endpointErr))
	previousErr := checkMCPNativePrevious(ctx, c, p, &s)
	checks = append(checks, check("previous_workers", previousErr))
	report := map[string]any{"stage": s.Stage, "servingDeployment": serving, "candidateDeployment": s.WebDeployment, "trafficChanged": changed, "candidateWorkersReady": candidateErr == nil, "candidateEndpointReady": endpointErr == nil, "observerReady": observerErr == nil, "previousWorkersReady": previousErr == nil, "checks": checks}
	if !resume {
		return jsonOut(writeJSON(report))
	}
	failed := make([]string, 0, len(checks))
	for _, item := range checks {
		if item.Status != "passed" && item.Name != "previous_workers" {
			failed = append(failed, item.Name+": "+item.Detail)
		}
	}
	if len(failed) > 0 {
		return printErr("MCP recovery", fmt.Errorf("resume blocked by failed readiness checks: %s", strings.Join(failed, "; ")))
	}
	return runMCPNativeRelease(p, s, state, plan)
}

func runMCPNativeRestore(p mcpNativeReleasePlan, s mcpNativeReleaseState, state, plan string) int {
	if !s.ServingCaptured || s.ServingDeployment == "" || s.WebDeployment == "" || s.PendingSubmission != "" || s.Stage == "complete" {
		return printErr("MCP restoration", errors.New("restore requires a captured, incomplete rollout without an uncertain submission"))
	}
	binary, err := os.Executable()
	if err != nil {
		return printErr("MCP restoration", err)
	}
	absolutePlan, err := filepath.Abs(plan)
	if err != nil {
		return printErr("MCP restoration", err)
	}
	args, _ := json.Marshal([]string{binary, "--json", "mcp", "tasks", "release", "restore-hook", "--plan", absolutePlan, "--state", state})
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.TimeoutSeconds+120)*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "tasks-release-restore.js", string(args), strconv.Itoa(p.TimeoutSeconds*1000))
	command.Dir = p.WorkerPath
	output, err := command.Output()
	var report struct {
		OK    bool   `json:"ok"`
		Stage string `json:"stage"`
	}
	if json.Unmarshal(output, &report) != nil {
		return printErr("MCP restoration", errors.New("restore gate unavailable; check Node dependencies and Task bindings"))
	}
	if code := jsonOut(writeJSON(report)); code != 0 {
		return code
	}
	if err != nil || !report.OK {
		return 1
	}
	return 0
}

func restoreMCPNativeWeb(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState, state string) error {
	if !s.ServingCaptured || s.ServingDeployment == "" || s.WebDeployment == "" || s.PendingSubmission != "" || s.Stage == "complete" {
		return errors.New("journal cannot restore an incomplete release")
	}
	current, err := mcpServingDeployment(ctx, c, p.WebApp, "")
	if err != nil {
		return err
	}
	restoring := s.Stage == "restore_pending" || s.Stage == "web_restored"
	if current != s.WebDeployment && (current != s.ServingDeployment || !restoring) {
		return errors.New("serving deployment changed; refusing restoration")
	}
	if err := mcpNativeObserverHealthy(ctx, c, p); err != nil {
		return err
	}
	if err := checkMCPNativePrevious(ctx, c, p, s); err != nil {
		return err
	}
	// Verify the exact previous artifact, including the configured OAuth and role policy.
	previous := *s
	previous.WebDeployment = s.ServingDeployment
	previous.ServingDeployment = current
	previous.Promoted = current == s.ServingDeployment
	if err := mcpNativeVerifyEndpoint(ctx, c, p, &previous); err != nil {
		return err
	}
	if current != s.ServingDeployment {
		s.Stage = "restore_pending"
		if err := saveMCPNativeState(state, s); err != nil {
			return err
		}
		if err := checkMCPNativePrevious(ctx, c, p, s); err != nil {
			return err
		}
		if _, err := c.PatchDeploymentTrafficIfServing(ctx, s.ServingDeployment, 100, s.WebDeployment); err != nil {
			actual, e := mcpServingDeployment(ctx, c, p.WebApp, "")
			if e != nil || actual != s.ServingDeployment {
				return err
			}
		}
	}
	previous.Promoted = true
	if err := mcpNativeVerifyEndpoint(ctx, c, p, &previous); err != nil {
		return err
	}
	s.Stage = "web_restored"
	s.Promoted = false
	return saveMCPNativeState(state, s)
}
