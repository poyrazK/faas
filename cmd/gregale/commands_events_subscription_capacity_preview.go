package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type subscriptionCapacityPreview struct {
	NewCommandReceiptSlotAvailable *bool                               `json:"new_command_receipt_slot_available,omitempty"`
	ReceiptBudgetWarning           string                              `json:"receipt_budget_warning,omitempty"`
	Expected                       *api.EventSubscriptionCapacityGuard `json:"expected,omitempty"`
	ReadOnly                       bool                                `json:"read_only"`
	Advisory                       bool                                `json:"advisory"`
	RequestedMaxPendingDeliveries  int                                 `json:"requested_max_pending_deliveries"`
	Valid                          bool                                `json:"valid"`
	ValidationError                string                              `json:"validation_error,omitempty"`
	Pressure                       api.EventConsumerCapacityPressure   `json:"pressure"`
	Projection                     *subscriptionCapacityProjection     `json:"projection,omitempty"`
}
type subscriptionCapacityProjection struct {
	EffectiveMaxPendingDeliveries int      `json:"effective_max_pending_deliveries"`
	RequiredConsumerDrain         int64    `json:"required_consumer_drain"`
	AvailableConsumerSlots        int64    `json:"available_consumer_slots"`
	BlockingScope                 string   `json:"blocking_scope"`
	SharedBlockingScopes          []string `json:"shared_blocking_scopes"`
	CapacityOnlyReady             bool     `json:"capacity_only_ready"`
}

func cmdEventsSubscriptionCapacityPreview(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events subscription-capacity-preview", flag.ContinueOnError)
	proposed := fs.Int("max-pending", -1, "proposed pending plus dispatching ceiling; 0 restores plan default")
	timeout := fs.Duration("timeout", api.EventSubscriptionControlTimeout, "total read deadline")
	maxAge := fs.Duration("max-age", api.EventDeliveryHealthWatchAgeDefault, "maximum pressure snapshot age")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || *proposed < 0 || *timeout <= 0 || *maxAge < api.EventDeliveryHealthWatchAgeMin || *maxAge > api.EventDeliveryHealthWatchAgeMax {
		PrintUsage(os.Stderr, "usage: gregale event subscription-capacity-preview APP SUB --max-pending N [--timeout 5s] [--max-age 30s]", "events")
		return 1
	}
	sub, err := uuid.Parse(positional[1])
	if err != nil || sub == uuid.Nil {
		return printErr("Invalid subscription ID", fmt.Errorf("subscription must be a nonzero UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	target, err := client.GetApp(ctx, positional[0])
	if err != nil {
		return capacityPreviewReadError(ctx, err)
	}
	app, err := uuid.Parse(target.ID)
	if err != nil || app == uuid.Nil {
		return printErr("Invalid app response", fmt.Errorf("app lookup lacks a stable UUID"))
	}
	health, err := client.GetEventConsumerDeliveryHealth(ctx, positional[0], sub.String(), "5m")
	if err != nil {
		return capacityPreviewReadError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return capacityPreviewReadError(ctx, err)
	}
	report, err := buildSubscriptionCapacityPreview(health.CapacityPressure, app.String(), sub.String(), *proposed, time.Now().UTC(), *maxAge)
	if err != nil {
		return printErr("Capacity preview unavailable", err)
	}
	code := 0
	if !report.Valid {
		code = 1
	}
	if jsonOutput {
		if output := jsonOut(writeJSON(report)); output != 0 {
			return output
		}
		return code
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | requested %d | plan ceiling %d | valid %t | observed %s\n", sub.String(), *proposed, report.Pressure.PlanMaxPendingDeliveries, report.Valid, report.Pressure.ObservedAt.Format(time.RFC3339Nano))
	printSubscriptionCapacityCommandBudget(report.Pressure.CapacityCommandBudget)
	if report.Projection == nil {
		_, _ = fmt.Fprintln(osStdout, report.ValidationError)
		return code
	}
	projected := report.Projection
	blocker := projected.BlockingScope
	if blocker == "" {
		blocker = "none observed"
	}
	_, _ = fmt.Fprintf(osStdout, "Current effective ceiling %d | proposed effective ceiling %d | live %d | consumer deliveries to drain %d | consumer slots %d\n", report.Pressure.Scopes[0].EffectiveLimit, projected.EffectiveMaxPendingDeliveries, report.Pressure.Scopes[0].LiveDeliveries, projected.RequiredConsumerDrain, projected.AvailableConsumerSlots)
	_, _ = fmt.Fprintf(osStdout, "Projected capacity blocker %s | shared blockers %v | capacity-only ready %t\n", blocker, projected.SharedBlockingScopes, projected.CapacityOnlyReady)
	for _, scope := range report.Pressure.Scopes[1:] {
		_, _ = fmt.Fprintf(osStdout, "%s live %d / %d\n", scope.Scope, scope.LiveDeliveries, scope.EffectiveLimit)
	}
	_, _ = fmt.Fprintf(osStdout, "Apply expectations: --expected-max-pending %d --expected-plan-max-pending %d (capacity apply still requires --yes)\n", *report.Expected.MaxPendingDeliveries, *report.Expected.PlanMaxPendingDeliveries)
	_, _ = fmt.Fprintln(osStdout, "Read-only advisory preview. No capacity is reserved; write permission/MFA, other delivery gates, immediate admission and ordering are not established.")
	return code
}
func capacityPreviewReadError(ctx context.Context, err error) int {
	code := printErr("Capacity preview read failed", err)
	if ctx.Err() == context.Canceled {
		return 130
	}
	return code
}

func buildSubscriptionCapacityPreview(pressure *api.EventConsumerCapacityPressure, app, sub string, proposed int, now time.Time, maxAge time.Duration) (subscriptionCapacityPreview, error) {
	out := subscriptionCapacityPreview{ReadOnly: true, Advisory: true, RequestedMaxPendingDeliveries: proposed}
	if pressure == nil || !pressure.Complete || pressure.Coverage != "live_slots_and_all_pending_application_recipients" || pressure.PlanMaxPendingDeliveries <= 0 || pressure.ConfiguredMaxPendingDeliveries < 0 || len(pressure.Scopes) != 3 || !sameBindingDeployment(pressure.AppID, app) || !sameBindingDeployment(pressure.SubscriptionID, sub) {
		return out, fmt.Errorf("missing or incompatible complete scoped pressure snapshot; upgrade older servers before previewing")
	}
	if pressure.ObservedAt.IsZero() || pressure.ObservedAt.After(now) || now.Sub(pressure.ObservedAt) > maxAge {
		return out, fmt.Errorf("pressure snapshot is stale or future-dated")
	}
	names := [3]string{"consumer", "app", "account"}
	blocker := ""
	for index, scope := range pressure.Scopes {
		if scope.Scope != names[index] || scope.LiveDeliveries < 0 || scope.EffectiveLimit <= 0 || scope.AtLimit != (scope.LiveDeliveries >= int64(scope.EffectiveLimit)) {
			return out, fmt.Errorf("invalid pressure usage or ceiling")
		}
		if scope.AtLimit && blocker == "" {
			blocker = scope.Scope
		}
		if index > 0 && (scope.LiveDeliveries < pressure.Scopes[index-1].LiveDeliveries || scope.EffectiveLimit < pressure.Scopes[index-1].EffectiveLimit) {
			return out, fmt.Errorf("inconsistent nested scope usage or limits")
		}
	}
	expected := pressure.PlanMaxPendingDeliveries
	if configured := pressure.ConfiguredMaxPendingDeliveries; configured > 0 && configured < expected {
		expected = configured
	}
	if pressure.Scopes[0].EffectiveLimit != expected || pressure.BlockingScope != blocker || pressure.PlanMaxPendingDeliveries > pressure.Scopes[1].EffectiveLimit {
		return out, fmt.Errorf("inconsistent plan ceiling or admission blocker")
	}
	if budget := pressure.CapacityCommandBudget; budget != nil {
		if err := validateSubscriptionCapacityCommandBudget(budget, app, sub, now, maxAge); err != nil {
			return out, err
		}
		if !budget.ObservedAt.Equal(pressure.ObservedAt) {
			return out, fmt.Errorf("receipt budget and pressure observations differ")
		}
		available := budget.RemainingSlots > 0
		out.NewCommandReceiptSlotAvailable = &available
		if budget.Full {
			out.ReceiptBudgetWarning = "New command UUIDs can be rejected while the receipt budget is full; known unchanged-input repeats remain available."
		}
	} else {
		out.ReceiptBudgetWarning = "Receipt budget unavailable; new command receipt-slot availability is unknown."
	}
	out.Pressure = *pressure
	if proposed < 0 || proposed > pressure.PlanMaxPendingDeliveries {
		out.ValidationError = fmt.Sprintf("proposed capacity must be between 0 and current plan ceiling %d", pressure.PlanMaxPendingDeliveries)
		return out, nil
	}
	configured, ceiling := pressure.ConfiguredMaxPendingDeliveries, pressure.PlanMaxPendingDeliveries
	out.Expected = &api.EventSubscriptionCapacityGuard{MaxPendingDeliveries: &configured, PlanMaxPendingDeliveries: &ceiling}
	out.Valid = true
	effective := proposed
	if effective == 0 {
		effective = pressure.PlanMaxPendingDeliveries
	}
	projection := &subscriptionCapacityProjection{EffectiveMaxPendingDeliveries: effective, SharedBlockingScopes: []string{}}
	live := pressure.Scopes[0].LiveDeliveries
	if live >= int64(effective) {
		projection.RequiredConsumerDrain = live - int64(effective) + 1
		projection.BlockingScope = "consumer"
	} else {
		projection.AvailableConsumerSlots = int64(effective) - live
	}
	for _, scope := range pressure.Scopes[1:] {
		if scope.AtLimit {
			projection.SharedBlockingScopes = append(projection.SharedBlockingScopes, scope.Scope)
			if projection.BlockingScope == "" {
				projection.BlockingScope = scope.Scope
			}
		}
	}
	projection.CapacityOnlyReady = projection.BlockingScope == ""
	out.Projection = projection
	return out, nil
}
