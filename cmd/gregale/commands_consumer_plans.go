package main

import (
	"context"
	"errors"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// defaultPlanName names the app-wide rate cards (no plan) on the command line.
const defaultPlanName = "default"

// callConsumerPlans handles the plan verbs of `gregale consumers` (ADR-940);
// handled is false for every other verb.
func callConsumerPlans(ctx context.Context, client *Client, verb string, args []string, f consumerFlags) (any, bool, error) {
	slug := args[0]
	switch verb {
	case "plans":
		out, err := client.ListAPIConsumerPlans(ctx, slug)
		return out, true, err
	case "plan-create":
		if f.maxPerMinute < -1 || f.maxPerMonth < -1 {
			return nil, true, errors.New("limits must be non-negative")
		}
		out, err := client.CreateAPIConsumerPlan(ctx, slug, api.CreateAPIConsumerPlanRequest{
			Name: f.name, MaxRequestsPerMinute: max(f.maxPerMinute, 0), MaxUnitsPerMonth: max(f.maxPerMonth, 0)})
		return out, true, err
	case "plan-update":
		out, err := updatePlanLimits(ctx, client, slug, f)
		return out, true, err
	case "set-plan":
		out, err := setConsumerPlan(ctx, client, slug, args[1], f)
		return out, true, err
	case "plan-history":
		out, err := client.ListAPIConsumerPlanAssignments(ctx, slug, args[1])
		return out, true, err
	}
	return nil, false, nil
}

// updatePlanLimits changes only the limits given; an omitted flag keeps the
// plan's current value.
func updatePlanLimits(ctx context.Context, client *Client, slug string, f consumerFlags) (any, error) {
	if f.plan == "" || f.plan == defaultPlanName {
		return nil, errors.New("--plan must name a consumer plan")
	}
	plans, err := client.ListAPIConsumerPlans(ctx, slug)
	if err != nil {
		return nil, err
	}
	for _, plan := range plans.Plans {
		if plan.Name != f.plan {
			continue
		}
		req := api.UpdateAPIConsumerPlanLimitsRequest{MaxRequestsPerMinute: plan.MaxRequestsPerMinute, MaxUnitsPerMonth: plan.MaxUnitsPerMonth}
		if f.maxPerMinute >= 0 {
			req.MaxRequestsPerMinute = f.maxPerMinute
		}
		if f.maxPerMonth >= 0 {
			req.MaxUnitsPerMonth = f.maxPerMonth
		}
		return client.UpdateAPIConsumerPlanLimits(ctx, slug, plan.ID, req)
	}
	return nil, fmt.Errorf("no consumer plan named %q", f.plan)
}

func setConsumerPlan(ctx context.Context, client *Client, slug, consumerID string, f consumerFlags) (any, error) {
	if f.plan == "" {
		return nil, errors.New("--plan is required; use \"default\" for the app default plan")
	}
	req := api.AssignAPIConsumerPlanRequest{}
	if f.plan != defaultPlanName {
		planID, err := resolvePlanID(ctx, client, slug, f.plan)
		if err != nil {
			return nil, err
		}
		req.PlanID = planID
	}
	if f.effectiveFrom != "" {
		at, err := time.Parse(time.RFC3339, f.effectiveFrom)
		if err != nil {
			return nil, fmt.Errorf("--effective-from: %w", err)
		}
		req.EffectiveFrom = &at
	}
	return client.AssignAPIConsumerPlan(ctx, slug, consumerID, req)
}

func resolvePlanID(ctx context.Context, client *Client, slug, name string) (string, error) {
	plans, err := client.ListAPIConsumerPlans(ctx, slug)
	if err != nil {
		return "", err
	}
	for _, plan := range plans.Plans {
		if plan.Name == name {
			return plan.ID, nil
		}
	}
	return "", fmt.Errorf("no consumer plan named %q", name)
}

func formatPlanLimit(limit int64, unit string) string {
	if limit == 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d %s", limit, unit)
}

func printConsumerPlanResult(tw *tabwriter.Writer, out any) bool {
	switch v := out.(type) {
	case api.APIConsumerPlanListResponse:
		_, _ = fmt.Fprintln(tw, "NAME\tREQUESTS PER MINUTE\tUNITS PER MONTH\tID")
		for _, plan := range v.Plans {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", plan.Name, formatPlanLimit(plan.MaxRequestsPerMinute, "requests"),
				formatPlanLimit(plan.MaxUnitsPerMonth, "units"), plan.ID)
		}
	case api.APIConsumerPlanResponse:
		_, _ = fmt.Fprintf(tw, "Plan\t%s\nRequests per minute\t%s\nUnits per month\t%s\nID\t%s\n", v.Name,
			formatPlanLimit(v.MaxRequestsPerMinute, "requests"), formatPlanLimit(v.MaxUnitsPerMonth, "units"), v.ID)
	case api.APIConsumerPlanAssignmentListResponse:
		_, _ = fmt.Fprintln(tw, "EFFECTIVE FROM\tPLAN ID")
		for _, a := range v.Assignments {
			_, _ = fmt.Fprintf(tw, "%s\t%s\n", a.EffectiveFrom.Format(time.RFC3339), planIDLabel(a.PlanID))
		}
	case api.APIConsumerPlanAssignmentResponse:
		_, _ = fmt.Fprintf(tw, "Consumer\t%s\nPlan\t%s\nEffective from\t%s\n", v.ConsumerID, planIDLabel(v.PlanID), v.EffectiveFrom.Format(time.RFC3339))
	default:
		return false
	}
	return true
}

func planIDLabel(planID string) string {
	if planID == "" {
		return defaultPlanName
	}
	return planID
}
