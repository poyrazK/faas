package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjectsEnvironmentRecoverMigration(args []string) int {
	flags, positional := splitArgsForFlags(args, "database-state-checked", "schema-compatible", "wait")
	fs := newFlagSet("projects-environments-recover-migration", flag.ContinueOnError)
	to := fs.String("to", "", "destination environment")
	action := fs.String("action", "", "resume the promotion or resolve it through a verified repair promotion")
	task := fs.String("task-id", "", "original migration task ID")
	generation := fs.Int64("generation", 0, "inspected promotion health generation")
	replacement := fs.String("replacement-promotion", "", "verified repair promotion ID (resolve only)")
	database := fs.Bool("database-state-checked", false, "attest that the destination database was inspected")
	compatible := fs.Bool("schema-compatible", false, "attest that the expected expansion is present and compatible (resume only)")
	evidence := fs.String("evidence-sha256", "", "SHA256 of private database inspection evidence; evidence contents are not uploaded")
	request := fs.String("request-id", "", "stable request ID; derived from the recovery request when omitted")
	wait := fs.Bool("wait", false, "wait for recovery confirmation; resumed promotion health continues afterward")
	timeout := secondsOrDurationFlag(fs, "timeout", defaultDeployWaitTimeoutSeconds, "maximum recovery wait in seconds or duration")
	if fs.Parse(flags) != nil || len(positional) != 2 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*to) || *timeout <= 0 || *timeout > api.ProjectPromotionMigrationRecoveryWaitMaxSeconds {
		return printErr("Invalid recovery options", fmt.Errorf("usage: gregale environment migration recover PROJECT PROMOTION --to ENV --action resume|resolve --task-id TASK --generation N --database-state-checked --evidence-sha256 HASH [--schema-compatible | --replacement-promotion ID]"))
	}
	req := api.ProjectPromotionMigrationRecoveryRequest{RequestID: *request, ExpectedTaskID: *task, ExpectedGeneration: *generation, Action: *action, ReplacementPromotionID: *replacement, DatabaseStateChecked: *database, SchemaCompatible: *compatible, EvidenceHash: *evidence}
	if req.RequestID == "" {
		raw, _ := json.Marshal(req)
		hash := fmt.Sprintf("%x", sha256.Sum256(raw))
		req.RequestID = hash[:32]
	}
	if !req.Valid() {
		return printErr("Invalid recovery request", fmt.Errorf("provide the inspected task/generation, database and schema attestations, evidence SHA256, and a replacement promotion for resolve"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()
	receipt, err := client.RecoverProjectEnvironmentPromotionMigration(ctx, positional[0], *to, positional[1], req)
	if err != nil {
		return printErr("Migration recovery failed", err)
	}
	if receipt.Request != req || receipt.OperatorAccountID == "" {
		return printErr("Invalid recovery acknowledgement", fmt.Errorf("server omitted the requested recovery identity"))
	}
	if *wait {
		receipt, err = waitForPromotionRecovery(ctx, client, positional[0], *to, positional[1], receipt)
		if err != nil {
			return printErr("Recovery wait interrupted", fmt.Errorf("request %s continues on the server; inspect projects environments status %s %s --to %s: %w", req.RequestID, positional[0], positional[1], *to, err))
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
	} else {
		renderPromotionRecovery(&receipt)
	}
	if receipt.Status == "blocked" {
		return 1
	}
	return 0
}
func waitForPromotionRecovery(ctx context.Context, client *Client, project, environment, promotion string, receipt api.ProjectPromotionMigrationRecovery) (api.ProjectPromotionMigrationRecovery, error) {
	for receipt.Status == "quiescing" || receipt.Status == "verifying" {
		timer := time.NewTimer(projectEnvironmentPromotionPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return receipt, ctx.Err()
		case <-timer.C:
		}
		current, err := client.GetProjectEnvironmentPromotionStatus(ctx, project, environment, promotion)
		if err != nil {
			return receipt, err
		}
		if current.Health == nil || current.Health.Recovery == nil || current.Health.Recovery.Request != receipt.Request {
			return receipt, fmt.Errorf("server recovery identity changed")
		}
		receipt = *current.Health.Recovery
	}
	return receipt, nil
}
func renderPromotionRecovery(r *api.ProjectPromotionMigrationRecovery) {
	_, _ = fmt.Fprintf(osStdout, "Migration recovery %s: %s (%s, task %s)\n", r.Request.RequestID, r.Status, r.Request.Action, r.Request.ExpectedTaskID)
	if r.Reason != "" {
		_, _ = fmt.Fprintln(osStdout, "  "+strings.ReplaceAll(r.Reason, "_", " "))
	}
	if r.Status == "resumed" {
		_, _ = fmt.Fprintln(osStdout, "  destination health checks resumed; the migration command was not replayed")
	}
	if r.Status == "resolved" {
		_, _ = fmt.Fprintln(osStdout, "  incident closed through the verified repair promotion; original task outcome retained")
	}
}
