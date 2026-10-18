package main

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateSubscriptionCapacityCommandBudget(b *api.EventSubscriptionCapacityCommandBudget, app, sub string, now time.Time, maxAge time.Duration) error {
	if b == nil || !b.Complete || !sameBindingDeployment(b.AppID, app) || !sameBindingDeployment(b.SubscriptionID, sub) || b.Limit != api.EventSubscriptionCapacityCommandMaxReceipts || b.RetainedReceipts < 0 || b.RetainedReceipts > b.Limit || b.RemainingSlots != b.Limit-b.RetainedReceipts || b.Full != (b.RemainingSlots == 0) {
		return fmt.Errorf("incompatible scoped receipt budget")
	}
	if b.ObservedAt.IsZero() || b.ObservedAt.After(now) || now.Sub(b.ObservedAt) > maxAge {
		return fmt.Errorf("receipt budget is stale or future-dated")
	}
	if (b.RetainedReceipts == 0) != (b.EarliestExpiryAt == nil) || (b.EarliestExpiryAt != nil && !b.EarliestExpiryAt.After(b.ObservedAt)) {
		return fmt.Errorf("invalid earliest receipt expiry")
	}
	return nil
}
func printSubscriptionCapacityCommandBudget(b *api.EventSubscriptionCapacityCommandBudget) {
	if b == nil {
		_, _ = fmt.Fprintln(osStdout, "Capacity command receipt budget unavailable; upgrade the server for diagnostics.")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Capacity command receipts %d / %d | remaining slots %d | full %t | observed %s\n", b.RetainedReceipts, b.Limit, b.RemainingSlots, b.Full, b.ObservedAt.Format(time.RFC3339Nano))
	if b.EarliestExpiryAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Earliest retained receipt expiry %s (advisory; concurrent commands can consume freed slots)\n", b.EarliestExpiryAt.Format(time.RFC3339))
	}
	if b.Full {
		_, _ = fmt.Fprintln(osStdout, "Warning: new command UUIDs can be rejected while the receipt budget is full. Known unchanged-input repeats remain available; commands without UUIDs have no idempotent receipt evidence.")
	}
}
