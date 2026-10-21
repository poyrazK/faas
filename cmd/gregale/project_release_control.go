package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjectDeployControl(action string, args []string) int {
	fs := newFlagSet("deploy "+action, flag.ContinueOnError)
	requestID := fs.String("request-id", "", "durable control request ID")
	generation := fs.Uint64("generation", 0, "inspected checkpoint generation")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidDeploymentAttemptID(fs.Arg(0)) {
		PrintUsage(os.Stderr, "usage: gregale deploy "+action+" <attempt-id> [--json]", "deploy")
		return 1
	}
	if action == "status" && (*requestID != "" || *generation != 0) {
		return printErr("Invalid flags", fmt.Errorf("status does not accept control flags"))
	}
	if *requestID != "" && !api.ValidDeploymentAttemptID(*requestID) {
		return printErr("Invalid request ID", fmt.Errorf("request ID must be 32 lowercase hexadecimal characters"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	attempt := fs.Arg(0)
	receipt, err := client.GetProjectSubmission(ctx, attempt)
	if err != nil {
		return printErr("Could not inspect deployment", err)
	}
	safe := receipt.Apply.SafeRelease
	if receipt.AttemptID != attempt || safe == nil || action != "status" && (safe.Verification == nil || !safe.Verification.Progressive || safe.Verification.Generation == 0) {
		return printErr("Unsupported deployment", fmt.Errorf("attempt does not support this deployment action"))
	}
	if action == "contract" && (safe.ReleaseCommand == nil || len(safe.ReleaseCommand.ContractCommandHash) != 64 || safe.ReleaseCommand.Contract == nil) {
		return printErr("Schema contract unavailable", fmt.Errorf("attempt has no staged cleanup command"))
	}
	if action != "status" {
		generationProvided := *generation != 0
		if *generation == 0 {
			*generation = safe.Verification.Generation
		}
		if action == "contract" && safe.ReleaseCommand != nil && safe.ReleaseCommand.Contract != nil && safe.ReleaseCommand.Contract.RequestID != "" && *requestID == "" {
			*requestID = safe.ReleaseCommand.Contract.RequestID
			if !generationProvided {
				*generation = safe.ReleaseCommand.Contract.ExpectedGeneration
			}
		}
		control := safe.Verification.Control
		if *requestID == "" && control != nil && control.Action == action && control.Status == "pending" {
			*requestID = control.RequestID
			if !generationProvided {
				*generation = control.ExpectedGeneration
			}
		}
		if *requestID == "" {
			*requestID = strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		// Preserve the identity on transport failure so the same decision can be retried.
		if !jsonOutput {
			fmt.Fprintf(os.Stderr, "Control request: %s (generation %d)\n", *requestID, *generation)
		}
		receipt, err = client.ControlProjectRelease(ctx, attempt, action, api.ProjectReleaseControlRequest{RequestID: *requestID, ExpectedGeneration: *generation})
		if err != nil {
			return printErr("Deployment control failed; retry with --request-id "+*requestID+fmt.Sprintf(" --generation %d", *generation), err)
		}
		safe = receipt.Apply.SafeRelease
		if receipt.AttemptID != attempt || safe == nil || safe.Verification == nil || !projectControlAcknowledged(safe, action, *requestID) {
			return printErr("Invalid control receipt", fmt.Errorf("server did not acknowledge the requested decision; inspect attempt %s", attempt))
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	verification := safe.Verification
	if verification == nil {
		verification = &api.ProjectReleaseVerification{}
	}
	if !verification.Progressive {
		_, _ = fmt.Fprintf(os.Stdout, "Deploy %s: %s\n", attempt, safe.Status)
	} else if verification.Generation == 0 {
		_, _ = fmt.Fprintf(os.Stdout, "Deploy %s: %s; progressive traffic has not started.\n", attempt, safe.Status)
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "Deploy %s: %s, stage %d%%, generation %d\n", attempt, safe.Status, verification.Percent, verification.Generation)
	}
	if command := safe.ReleaseCommand; command != nil {
		_, _ = fmt.Fprintf(os.Stdout, "Migration: %s, task %s\n", command.Status, command.TaskID)
		if contract := command.Contract; contract != nil {
			_, _ = fmt.Fprintf(os.Stdout, "Schema contract: %s, reason %s, task %s, retired deployments %d\n", contract.Status, contract.Reason, contract.TaskID, contract.RetiredDeployments)
			if recovery := contract.Recovery; recovery != nil {
				_, _ = fmt.Fprintf(os.Stdout, "Cleanup recovery: %s, reason %s, replacement %s\n", recovery.Status, recovery.Reason, recovery.ReplacementAttemptID)
			}
		}
		if command.VerifyBaseline {
			status := "waiting"
			if proof := command.BaselineVerification; proof != nil {
				status = proof.Status
			}
			_, _ = fmt.Fprintf(os.Stdout, "Post-expansion baseline: %s\n", status)
		}
	}
	if v := verification.Stabilization; v != nil && v.Enabled {
		health := v.Health
		if health == "" {
			health = "unknown"
		}
		_, _ = fmt.Fprintf(os.Stdout, "Stabilization: %s, health %s\n", v.Status, health)
	}
	if control := verification.Control; control != nil {
		_, _ = fmt.Fprintf(os.Stdout, "Control: %s (%s), request %s\n", control.Action, control.Status, control.RequestID)
	}
	if safe.Message != "" {
		_, _ = fmt.Fprintln(os.Stdout, safe.Message)
	}
	if len(verification.Actions) > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "Available actions: %s\n", strings.Join(verification.Actions, ", "))
	}
	return 0
}

func projectControlAcknowledged(safe *api.ProjectSafeRelease, action, request string) bool {
	if action == "contract" {
		return safe.ReleaseCommand != nil && safe.ReleaseCommand.Contract != nil && safe.ReleaseCommand.Contract.RequestID == request && safe.ReleaseCommand.Contract.TaskID != ""
	}
	c := safe.Verification.Control
	return c != nil && c.RequestID == request && c.Action == action
}
