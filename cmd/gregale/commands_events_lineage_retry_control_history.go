package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsLineageRetryJobControls(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("subscription-lineage-retry-job-controls", flag.ContinueOnError)
	expected := fs.String("expected-created-at", "", "required original job creation timestamp")
	after := fs.Int64("after", 0, "exclusive command sequence")
	limit := fs.Int("limit", api.EventExecutionLineageRetryControlPageDefault, "page size (maximum 200)")
	action := fs.String("action", "", "exact command action: pause, resume or cancel")
	actorID := fs.String("actor-id", "", "exact actor UUID or store for internal calls")
	credential := fs.String("credential-type", "", "exact original credential type")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 3 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events subscription-lineage-retry-job-controls APP SUB REQUEST_UUID --expected-created-at TIMESTAMP [--after SEQUENCE] [--limit N] [--action ACTION] [--actor-id UUID_OR_STORE] [--credential-type TYPE]", "events")
		return 1
	}
	at, err := time.Parse(time.RFC3339Nano, *expected)
	if err != nil || at.IsZero() {
		return printErr("Invalid history guard", fmt.Errorf("expected-created-at must be the original RFC3339 job creation timestamp"))
	}
	filter, err := (api.EventExecutionLineageRetryJobControlFilters{Action: *action, ActorID: *actorID, CredentialType: *credential}).Normalize()
	if err != nil {
		return printErr("Invalid history filters", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventSubscriptionControlTimeout)
	defer cancel()
	out, err := client.ListEventExecutionLineageRetryJobControls(ctx, positional[0], positional[1], positional[2], at, *after, *limit, filter)
	if err != nil {
		return printErr("Control history unavailable", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Controls) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No retained command receipts match these filters after this sequence.")
	}
	for _, receipt := range out.Controls {
		_, _ = fmt.Fprintf(osStdout, "Control %s | sequence %d | %s | recorded result %s | actor %s | processed %d/%d | recorded %s\n", receipt.CommandID, receipt.Sequence, receipt.Action, receipt.ResultStatus, lineageControlReceiptActor(receipt), receipt.Processed, receipt.Selected, receipt.RecordedAt.Format(time.RFC3339Nano))
	}
	if out.NextAfter > 0 {
		_, _ = fmt.Fprintf(osStdout, "Next page: --after %d (keep the same filters)\n", out.NextAfter)
	}
	return 0
}

func lineageControlReceiptActor(receipt api.EventExecutionLineageRetryJobControlReceipt) string {
	if receipt.ActorKind == "" || receipt.ActorID == "" || receipt.CredentialType == "" {
		return "unknown (legacy receipt)"
	}
	return oneLine(receipt.ActorKind) + ":" + oneLine(receipt.ActorID) + " (" + oneLine(receipt.CredentialType) + ")"
}
