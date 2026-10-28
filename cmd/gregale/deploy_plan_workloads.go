package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// deployWorkloadPlan is `deploy --plan` for a source with several workloads
// (ADR-20261026145832): the local split `scan` shows after upload.
type deployWorkloadPlan struct {
	Inference     string              `json:"inference"`
	Workloads     []api.PlanWorkload  `json:"workloads"`
	Managed       []deployPlanManaged `json:"managed,omitempty"`
	DeployCommand string              `json:"deploy_command"`
	Warnings      []string            `json:"warnings,omitempty"`
}

type deployPlanManaged struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Source string `json:"source,omitempty"`
}

func printDeployWorkloadPlan(scan reposcan.Result) int {
	plan := deployWorkloadPlan{Inference: "local", DeployCommand: "gregale deploy --project", Warnings: scan.Warnings}
	for _, workload := range scan.Workloads {
		plan.Workloads = append(plan.Workloads, reposcan.PlanWorkload(workload))
	}
	for _, managed := range scan.Managed {
		plan.Managed = append(plan.Managed, deployPlanManaged{Name: managed.Name, Kind: managed.Kind, Source: managed.Source})
	}
	if jsonOutput {
		return jsonOut(writeJSON(plan))
	}
	_, _ = fmt.Fprintf(osStdout, "Plan (local inference; no login, upload or remote state)\n\nWorkloads (%d):\n", len(plan.Workloads))
	for _, workload := range plan.Workloads {
		dir := workload.RootDir
		if dir == "" {
			dir = "."
		}
		_, _ = fmt.Fprintf(osStdout, "  %-20s %s\n", workload.Name, dir)
	}
	if len(plan.Managed) > 0 {
		_, _ = fmt.Fprintln(osStdout, "Managed resources:")
		for _, managed := range plan.Managed {
			_, _ = fmt.Fprintf(osStdout, "  %-20s %s\n", managed.Name, managed.Kind)
		}
	}
	for _, warning := range plan.Warnings {
		_, _ = fmt.Fprintf(osStdout, "Warning: %s\n", warning)
	}
	_, _ = fmt.Fprintf(osStdout, "\nDeploy them together: %s\nServer-side detail: gregale scan\n", plan.DeployCommand)
	return 0
}
