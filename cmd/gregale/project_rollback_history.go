package main

import (
	"context"
	"errors"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func protectProjectRollbackDeploy(ctx context.Context, client *api.Client, slug, deploy string, ttlSeconds int) int {
	hold, err := client.ProtectProjectRollbackDeploy(ctx, slug, deploy, api.ProtectProjectRollbackDeployRequest{TTLSeconds: ttlSeconds})
	if err != nil {
		return printErr("Could not protect project deploy", err)
	}
	if hold.DeployGroupID != deploy || hold.ExpiresAt.IsZero() || hold.LayerBytes <= 0 {
		return printErr("Invalid project rollback protection", errors.New("server returned an invalid protection receipt"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(hold))
	}
	PrintOK(osStdout, "Deploy %s protected until %s (%s of layers)", hold.DeployGroupID,
		hold.ExpiresAt.UTC().Format(time.RFC3339), formatIECBytes(hold.LayerBytes))
	return 0
}

func showProjectRollbackTargets(ctx context.Context, client *api.Client, slug string) int {
	targets, err := client.ListProjectRollbackTargets(ctx, slug)
	if err != nil {
		return printErr("Could not read project rollback targets", err)
	}
	if targets.Project != slug || targets.ProjectID == "" {
		return printErr("Invalid project rollback targets", errors.New("server returned deploys for a different project"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(targets))
	}
	if len(targets.Deploys) == 0 {
		PrintProgress(osStdout, "No recorded project deploys available to preview.")
		return 0
	}
	table := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "DEPLOY ID\tSERVICES\tCREATED AT (UTC)\tPROTECTED UNTIL (UTC)\tLAYER BYTES")
	for _, deploy := range targets.Deploys {
		expiry, size := "-", "-"
		if hold := deploy.Protection; hold != nil {
			expiry, size = hold.ExpiresAt.UTC().Format(time.RFC3339), formatIECBytes(hold.LayerBytes)
		}
		_, _ = fmt.Fprintf(table, "%s\t%d\t%s\t%s\t%s\n", deploy.DeployGroupID, deploy.ServiceCount, deploy.CreatedAt.UTC().Format(time.RFC3339), expiry, size)
	}
	if err := table.Flush(); err != nil {
		return printErr("Could not write project rollback targets", err)
	}
	return 0
}

func showProjectRollbackHistory(ctx context.Context, client *api.Client, slug string) int {
	history, err := client.ListProjectRollbacks(ctx, slug)
	if err != nil {
		return printErr("Could not read project rollback history", err)
	}
	if history.Project != slug || history.ProjectID == "" {
		return printErr("Invalid project rollback history", errors.New("server returned history for a different project"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(history))
	}
	if len(history.Operations) == 0 {
		PrintProgress(osStdout, "No project rollbacks or undos recorded.")
		return 0
	}
	table := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "OPERATION ID\tSTATUS\tCUTOVERS\tSTARTED AT (UTC)\tRELATED OPERATION")
	for _, op := range history.Operations {
		status := op.Status
		if op.PauseRequestedAt != nil {
			switch status {
			case "blocked":
				status = "paused"
			case "running":
				status = "pausing"
			}
		}
		started := "-"
		if op.CreatedAt != nil {
			started = op.CreatedAt.UTC().Format(time.RFC3339)
		}
		related := "-"
		if op.UndoOf != "" {
			related = "undo of " + op.UndoOf
		} else if op.UndoOperationID != "" {
			related = "undo " + op.UndoOperationID
		}
		_, _ = fmt.Fprintf(table, "%s\t%s\t%d/%d\t%s\t%s\n", op.ID, status,
			op.CutoversCompleted, op.CutoversTotal, started, related)
	}
	if err := table.Flush(); err != nil {
		return printErr("Could not write project rollback history", err)
	}
	return 0
}
