package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjectsEnvironmentControl(action string, args []string) int {
	flags, positional := splitArgsForFlags(args, "wait")
	fs := newFlagSet("projects-environments-"+action, flag.ContinueOnError)
	to := fs.String("to", "", "destination environment")
	generation := fs.Int64("generation", 0, "inspected promotion health generation")
	request := fs.String("request-id", "", "stable request ID; derived from promotion, action and generation when omitted")
	wait := fs.Bool("wait", false, "wait for this operator decision to finish")
	timeout := secondsOrDurationFlag(fs, "timeout", defaultDeployWaitTimeoutSeconds, "maximum wait in seconds or duration")
	if fs.Parse(flags) != nil || len(positional) != 2 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(*to) || *generation <= 0 || *timeout <= 0 || *timeout > api.ProjectPromotionMigrationRecoveryWaitMaxSeconds {
		return printErr("Invalid promotion control options", fmt.Errorf("usage: gregale projects environments %s PROJECT PROMOTION --to ENV --generation N [--request-id ID] [--wait]", action))
	}
	req := api.ProjectPromotionControlRequest{RequestID: *request, ExpectedGeneration: *generation}
	if req.RequestID == "" {
		digest := sha256.Sum256([]byte(fmt.Sprintf("promotion-control\x00%s\x00%s\x00%s\x00%s\x00%d", positional[0], *to, positional[1], action, *generation)))
		req.RequestID = fmt.Sprintf("%x", digest[:16])
	}
	if !req.Valid() {
		return printErr("Invalid request ID", fmt.Errorf("request ID must be 32 lowercase hexadecimal characters"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()
	receipt, err := client.ControlProjectEnvironmentPromotion(ctx, positional[0], *to, positional[1], action, req)
	if err != nil {
		return printErr("Promotion control failed", err)
	}
	if !promotionControlReceiptMatches(receipt, action, req) {
		return printErr("Invalid control acknowledgement", fmt.Errorf("server omitted the requested decision identity"))
	}
	if *wait {
		// Status polls avoid repeating write audits. If a newer decision replaced the
		// current one, replay reads this request's immutable terminal receipt.
		for receipt.Status == "pending" {
			timer := time.NewTimer(projectEnvironmentPromotionPollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				err = ctx.Err()
			case <-timer.C:
				current, readErr := client.GetProjectEnvironmentPromotionStatus(ctx, positional[0], *to, positional[1])
				err = readErr
				if err == nil {
					if current.Health != nil && current.Health.Control != nil && current.Health.Control.Request == req && current.Health.Control.Action == action {
						receipt = *current.Health.Control
					} else {
						receipt, err = client.ControlProjectEnvironmentPromotion(ctx, positional[0], *to, positional[1], action, req)
					}
				}
			}
			if err != nil {
				return printErr("Control wait interrupted", fmt.Errorf("request %s continues on the server; inspect promotion status: %w", req.RequestID, err))
			}
			if !promotionControlReceiptMatches(receipt, action, req) {
				return printErr("Invalid control acknowledgement", fmt.Errorf("server control identity changed"))
			}
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Promotion %s %s: %s (request %s)\n", positional[1], action, receipt.Status, receipt.Request.RequestID)
		if receipt.Reason != "" {
			_, _ = fmt.Fprintln(osStdout, "  "+receipt.Reason)
		}
	}
	if receipt.Status == "blocked" {
		return 1
	}
	return 0
}

func promotionControlReceiptMatches(c api.ProjectPromotionControl, action string, req api.ProjectPromotionControlRequest) bool {
	return c.Request == req && c.Action == action && c.OperatorAccountID != "" && (c.Status == "pending" || c.Status == "aborted" && action == "abort" || c.Status == "restored" || c.Status == "blocked")
}

func promotionControlCLIFlags() []cliFlag {
	return []cliFlag{
		{Name: "to", Value: "ENV", Short: "destination environment"},
		{Name: "generation", Value: "N", Short: "inspected promotion health generation"},
		{Name: "request-id", Value: "ID", Short: "stable 32-character lowercase hexadecimal request ID"},
		{Name: "wait", Bool: true, Short: "wait for this decision to finish"},
		{Name: "timeout", Value: "SECONDS|DURATION", Short: "maximum local wait; server work continues"},
	}
}
