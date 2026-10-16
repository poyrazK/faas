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

func cmdProjectMigrationRecovery(args []string) int {
	fs := newFlagSet("deploy resolve-contract", flag.ContinueOnError)
	replacement := fs.String("replacement-attempt", "", "verified corrected deployment attempt")
	task := fs.String("task-id", "", "inspected cleanup task ID")
	generation := fs.Uint64("generation", 0, "inspected cleanup generation")
	request := fs.String("request-id", "", "durable recovery request ID; reuse after a lost response")
	evidence := fs.String("evidence-hash", "", "SHA256 of private database/execution recovery evidence")
	checked := fs.Bool("database-state-checked", false, "attest that the database state was inspected and repaired")
	quiescent := fs.Bool("execution-quiescent", false, "attest that the original cleanup execution has stopped")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidDeploymentAttemptID(fs.Arg(0)) {
		PrintUsage(os.Stderr, "usage: gregale deploy resolve-contract <attempt-id> --replacement-attempt ID --database-state-checked --execution-quiescent --evidence-hash SHA256", "deploy")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	attempt := fs.Arg(0)
	receipt, err := client.GetProjectSubmission(ctx, attempt)
	if err != nil {
		return printErr("Could not inspect cleanup", err)
	}
	safe := receipt.Apply.SafeRelease
	if receipt.AttemptID != attempt || safe == nil || safe.ReleaseCommand == nil || safe.ReleaseCommand.Contract == nil {
		return printErr("Recovery unavailable", fmt.Errorf("attempt has no cleanup receipt"))
	}
	contract := safe.ReleaseCommand.Contract
	if *task == "" {
		*task = contract.TaskID
	}
	if *generation == 0 {
		*generation = contract.ExpectedGeneration
	}
	if saved := contract.Recovery; saved != nil && saved.Status == "resolved" {
		if *request == "" {
			*request = saved.RequestID
		}
		if *replacement == "" {
			*replacement = saved.ReplacementAttemptID
		}
		if *evidence == "" {
			*evidence = saved.EvidenceHash
		}
	}
	if *request == "" {
		*request = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	req := api.ProjectMigrationRecoveryRequest{RequestID: *request, ExpectedTaskID: *task, ExpectedGeneration: *generation, ReplacementAttemptID: *replacement, DatabaseStateChecked: *checked, ExecutionQuiescent: *quiescent, EvidenceHash: *evidence}
	if !req.Valid() || *replacement == attempt {
		return printErr("Recovery checks required", fmt.Errorf("supply a corrected verified attempt, both explicit database/execution attestations, and a lowercase SHA256 evidence hash"))
	}
	_, _ = fmt.Fprintf(os.Stderr, "Recovery request: %s (task %s, generation %d)\n", *request, *task, *generation)
	receipt, err = client.ResolveProjectMigrationContract(ctx, attempt, req)
	if err != nil {
		return printErr("Recovery failed; retry with --request-id "+*request, err)
	}
	if receipt.AttemptID != attempt || receipt.Apply.SafeRelease == nil || receipt.Apply.SafeRelease.ReleaseCommand == nil || receipt.Apply.SafeRelease.ReleaseCommand.Contract == nil {
		return printErr("Invalid recovery receipt", fmt.Errorf("server omitted the cleanup outcome"))
	}
	recovery := receipt.Apply.SafeRelease.ReleaseCommand.Contract.Recovery
	if recovery == nil || recovery.ReplacementReleaseID == "" || recovery.OperatorAccountID == "" || recovery.ResolvedAt == nil || recovery.Status != "resolved" || recovery.RequestID != *request || recovery.TaskID != *task || recovery.ExpectedGeneration != *generation || recovery.ReplacementAttemptID != *replacement || recovery.EvidenceHash != *evidence {
		return printErr("Invalid recovery acknowledgement", fmt.Errorf("server did not bind the requested resolution"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	_, _ = fmt.Fprintf(os.Stdout, "Cleanup recovery resolved through deploy %s; original task %s remains %s.\n", recovery.ReplacementAttemptID, recovery.TaskID, receipt.Apply.SafeRelease.ReleaseCommand.Contract.Status)
	return 0
}
