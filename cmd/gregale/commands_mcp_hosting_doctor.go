package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func printMCPHostingDoctor(status mcpTasksStatusResult, preflightPath string) int {
	report := struct {
		App                  string             `json:"app_slug"`
		OK                   bool               `json:"ok"`
		Checks               []mcphosting.Check `json:"checks"`
		HandlerCompatibility json.RawMessage    `json:"handlerCompatibility,omitempty"`
		TaskAdmission        json.RawMessage    `json:"taskAdmission,omitempty"`
	}{App: status.AppSlug, OK: true, Checks: []mcphosting.Check{}}
	add := func(name, state, detail string) {
		report.Checks = append(report.Checks, mcphosting.Check{Name: name, Status: state, Detail: detail})
		if state != "passed" {
			report.OK = false
		}
	}
	if status.Configured {
		add("scaling_policy", "passed", "Task scaling policy matches the deployment manifest")
	} else {
		add("scaling_policy", "failed", "Run gregale mcp tasks setup --app "+status.AppSlug+" --apply")
	}
	for _, metric := range status.Metrics {
		if metric.Name == "mcp_tasks_observer_heartbeat" && !status.ScaleToZeroConfigured {
			continue
		}
		if !metric.Present || metric.Stale {
			add(metric.Name, "unknown", "Publish a fresh metric before gating deployment")
		} else {
			add(metric.Name, "passed", "Fresh server-reported observation")
		}
	}
	if status.ScaleToZeroConfigured && !status.ObserverHeartbeatFresh {
		add("scale_to_zero_observer", "failed", "Restore the always-on observer heartbeat before allowing zero workers")
	}
	for _, diagnostic := range status.Diagnostics {
		switch diagnostic.Code {
		case "unsupported_handler", "no_active_workers", "idle_queue":
			add(diagnostic.Code, "failed", diagnostic.Message)
		}
	}
	if preflightPath == "" {
		add("database_and_keys", "unknown", "Use --preflight-path <generated-starter> with deployment bindings to check schema permissions and encryption keys")
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "node", "task-doctor.js")
		command.Dir = preflightPath
		output, runErr := command.Output()
		var local struct {
			OK                   bool               `json:"ok"`
			Checks               []mcphosting.Check `json:"checks"`
			HandlerCompatibility json.RawMessage    `json:"handlerCompatibility,omitempty"`
			TaskAdmission        json.RawMessage    `json:"taskAdmission,omitempty"`
		}
		if json.Unmarshal(output, &local) != nil || len(local.Checks) == 0 {
			add("database_and_keys", "failed", "Could not run Task preflight; check Node dependencies and deployment bindings")
		} else {
			report.HandlerCompatibility = local.HandlerCompatibility
			report.TaskAdmission = local.TaskAdmission
			for _, check := range local.Checks {
				add(check.Name, check.Status, check.Detail)
			}
			if runErr != nil || !local.OK {
				report.OK = false
			}
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		if len(report.TaskAdmission) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Task admission: %s\n", report.TaskAdmission)
		}
		if len(report.HandlerCompatibility) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Retained handler compatibility: %s\n", report.HandlerCompatibility)
		}
		for _, check := range report.Checks {
			_, _ = fmt.Fprintf(osStdout, "%s: %s — %s\n", check.Name, check.Status, check.Detail)
		}
	}
	if !report.OK {
		return 1
	}
	return 0
}
