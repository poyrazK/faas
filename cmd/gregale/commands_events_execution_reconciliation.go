package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsExecutionReconciliation(args []string, request bool) int {
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale events subscription-execution-reconciliation <app> <subscription-id> | subscription-reconcile-execution <app> <subscription-id>", "events")
		return 1
	}
	if _, err := uuid.Parse(args[1]); err != nil {
		return printErr("Invalid subscription ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.EventExecutionReconciliation
	if request {
		out, err = client.RequestEventExecutionReconciliation(context.Background(), args[0], args[1])
	} else {
		out, err = client.GetEventExecutionReconciliation(context.Background(), args[0], args[1])
	}
	if err != nil {
		return printErr("Execution reconciliation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | %s | requested %v | collection start %v | unattributed execution present %t\n", oneLine(out.SubscriptionID), oneLine(out.Status), out.RequestedAt, out.CollectionStartedAt, out.UnattributedExecutionPresent)
	_, _ = fmt.Fprintf(osStdout, "Repair scanned %d | repaired %d | unresolved %d | conflicts %d\n", out.RepairScanned, out.RepairRepaired, out.RepairUnresolved, out.RepairConflicts)
	return 0
}
