package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// platformTenantPlanVerbs are the tenant plan verbs of `gregale
// platform-tenants` (ADR-1307), mapped to the flags each accepts.
var platformTenantPlanVerbs = map[string][]string{
	"plans": {},
	"plan-create": {"name", "currency", "price-millicents", "included-units", "tier", "weight", "max-requests-per-minute", "max-requests-per-day", "unit", "tier-mode",
		"base-fee-millicents", "minimum-millicents", "prorate-monthly-fees"},
	"plan-update": {"plan", "name", "currency", "price-millicents", "included-units", "tier", "weight", "max-requests-per-minute", "max-requests-per-day", "unit", "tier-mode",
		"base-fee-millicents", "minimum-millicents", "prorate-monthly-fees"},
	"set-plan":     {"id", "plan"},
	"plan-history": {"id"},
}

type platformTenantPlanFlags struct {
	id, plan, name, currency string
	unit, tierMode           string
	priceMillicents          int64
	includedUnits            int64
	tiers, weights           multiFlag
	maxPerMinute, maxPerDay  int64
	baseFee, minimum         int64
	prorate                  bool
	set                      map[string]bool
}

// cmdPlatformTenantPlans handles the plan verbs; handled is false for every
// other platform-tenants verb.
func cmdPlatformTenantPlans(verb string, args []string) (handled bool, code int) {
	allowed, ok := platformTenantPlanVerbs[verb]
	if !ok {
		return false, 0
	}
	fs := newFlagSet("platform-tenants "+verb, flag.ContinueOnError)
	var f platformTenantPlanFlags
	fs.StringVar(&f.id, "id", "", "platform tenant UUID")
	fs.StringVar(&f.plan, "plan", "", "tenant plan name")
	fs.StringVar(&f.name, "name", "", "plan name (lowercase letters, digits, hyphens)")
	fs.StringVar(&f.currency, "currency", "", "ISO-4217 currency, e.g. EUR")
	fs.Int64Var(&f.priceMillicents, "price-millicents", 0, "price per request unit in millicents; 100000 = 1.00")
	fs.Int64Var(&f.includedUnits, "included-units", 0, "free units per tenant per UTC calendar month")
	fs.Var(&f.tiers, "tier", "graduated step UP_TO:PRICE_MILLICENTS, repeatable; the last step's UP_TO is inf")
	fs.Var(&f.weights, "weight", "route weight \"METHOD /template=N\", repeatable")
	fs.StringVar(&f.unit, "unit", "", "what the plan's card prices: request (default), compute_second, response_megabyte or app:<meter>")
	fs.Int64Var(&f.baseFee, "base-fee-millicents", 0, "fixed charge per tenant per UTC month; 100000 = 1.00")
	fs.Int64Var(&f.minimum, "minimum-millicents", 0, "minimum usage charge per tenant per UTC month, topped up when usage costs less")
	fs.BoolVar(&f.prorate, "prorate-monthly-fees", false, "scale the fee and minimum in a tenant's first month to the share left after it was created")
	fs.StringVar(&f.tierMode, "tier-mode", "", "how --tier steps price a month: graduated (default) or volume")
	fs.Int64Var(&f.maxPerMinute, "max-requests-per-minute", -1, "request budget per minute the plan sets; 0 disables the ceiling")
	fs.Int64Var(&f.maxPerDay, "max-requests-per-day", -1, "request budget per day the plan sets; 0 disables the ceiling")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, 0
		}
		return true, 1
	}
	f.set = map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
	needsID := verb == "set-plan" || verb == "plan-history"
	needsPlan := verb == "set-plan" || verb == "plan-update"
	if fs.NArg() != 0 || !flagsWithin(fs, allowed) || (needsID && f.id == "") || (needsPlan && f.plan == "") {
		PrintUsage(osStderr, platformTenantBillingUsage(verb, allowed), "platform-tenants")
		return true, 1
	}
	client, err := authedClient()
	if err != nil {
		return true, printErr("Not logged in", err)
	}
	out, err := callPlatformTenantPlans(context.Background(), client, verb, f)
	if err != nil {
		return true, printErr("Platform tenant "+verb+" failed", err)
	}
	if jsonOutput {
		return true, jsonOut(writeJSON(out))
	}
	printPlatformTenantPlans(out)
	return true, 0
}

func callPlatformTenantPlans(ctx context.Context, client *Client, verb string, f platformTenantPlanFlags) (any, error) {
	switch verb {
	case "plans":
		return client.ListPlatformTenantPlans(ctx)
	case "plan-create":
		req, err := applyPlanFlags(api.PlatformTenantPlanRequest{Currency: "EUR"}, f)
		if err != nil {
			return nil, err
		}
		return client.CreatePlatformTenantPlan(ctx, req)
	case "plan-update":
		plan, err := findTenantPlan(ctx, client, f.plan)
		if err != nil {
			return nil, err
		}
		req, err := applyPlanFlags(api.PlatformTenantPlanRequest{Name: plan.Name, Currency: plan.Currency,
			PriceMillicentsPerUnit: plan.PriceMillicentsPerUnit, IncludedUnitsPerMonth: plan.IncludedUnitsPerMonth,
			Tiers: plan.Tiers, RouteWeights: plan.RouteWeights, RequestBudget: plan.RequestBudget, Unit: plan.Unit, TierMode: plan.TierMode,
			BaseFeeMillicentsPerMonth: plan.BaseFeeMillicentsPerMonth, MinimumMillicentsPerMonth: plan.MinimumMillicentsPerMonth,
			ProrateMonthlyFees: plan.ProrateMonthlyFees}, f)
		if err != nil {
			return nil, err
		}
		return client.UpdatePlatformTenantPlan(ctx, plan.ID, req)
	case "set-plan":
		plan, err := findTenantPlan(ctx, client, f.plan)
		if err != nil {
			return nil, err
		}
		return client.AssignPlatformTenantPlan(ctx, f.id, api.AssignPlatformTenantPlanRequest{PlanID: plan.ID})
	case "plan-history":
		return client.ListPlatformTenantPlanAssignments(ctx, f.id)
	}
	return nil, fmt.Errorf("unknown platform-tenants verb %q", verb)
}

// applyPlanFlags overrides req with the flags given; plan-update keeps the
// plan's other terms.
func applyPlanFlags(req api.PlatformTenantPlanRequest, f platformTenantPlanFlags) (api.PlatformTenantPlanRequest, error) {
	if f.set["name"] {
		req.Name = strings.TrimSpace(f.name)
	}
	if f.set["currency"] {
		req.Currency = strings.ToUpper(strings.TrimSpace(f.currency))
	}
	if f.set["price-millicents"] {
		req.PriceMillicentsPerUnit, req.Tiers = f.priceMillicents, nil
	}
	if f.set["included-units"] {
		req.IncludedUnitsPerMonth, req.Tiers = f.includedUnits, nil
	}
	if f.set["tier"] {
		tiers, err := parseRateCardTiers(f.tiers)
		if err != nil {
			return req, err
		}
		req.Tiers, req.PriceMillicentsPerUnit, req.IncludedUnitsPerMonth = tiers, 0, 0
	}
	if f.set["weight"] {
		weights, err := parseRouteWeights(f.weights)
		if err != nil {
			return req, err
		}
		req.RouteWeights = weights
	}
	if f.set["unit"] {
		req.Unit = strings.TrimSpace(f.unit)
	}
	if f.set["tier-mode"] {
		req.TierMode = strings.TrimSpace(f.tierMode)
	}
	if f.set["base-fee-millicents"] {
		req.BaseFeeMillicentsPerMonth = f.baseFee
	}
	if f.set["minimum-millicents"] {
		req.MinimumMillicentsPerMonth = f.minimum
	}
	if f.set["prorate-monthly-fees"] {
		req.ProrateMonthlyFees = f.prorate
	}
	if f.set["max-requests-per-minute"] != f.set["max-requests-per-day"] {
		return req, errors.New("set --max-requests-per-minute and --max-requests-per-day together")
	}
	if f.set["max-requests-per-minute"] {
		req.RequestBudget = &api.PlatformTenantPlanBudget{MaxRequestsPerMinute: f.maxPerMinute, MaxRequestsPerDay: f.maxPerDay}
	}
	if req.Name == "" {
		return req, errors.New("--name is required")
	}
	return req, nil
}

func findTenantPlan(ctx context.Context, client *Client, name string) (api.PlatformTenantPlanResponse, error) {
	plans, err := client.ListPlatformTenantPlans(ctx)
	if err != nil {
		return api.PlatformTenantPlanResponse{}, err
	}
	for _, plan := range plans.Plans {
		if plan.Name == name {
			return plan, nil
		}
	}
	return api.PlatformTenantPlanResponse{}, fmt.Errorf("no tenant plan named %q", name)
}

func printPlatformTenantPlans(out any) {
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	switch v := out.(type) {
	case api.PlatformTenantPlanListResponse:
		_, _ = fmt.Fprintln(tw, "NAME\tPRICE\tINCLUDED/MONTH\tWEIGHTED ROUTES\tBUDGET (MIN/DAY)")
		for _, p := range v.Plans {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", p.Name, formatTenantPlanPrice(p), p.IncludedUnitsPerMonth, len(p.RouteWeights), formatPlanBudget(p.RequestBudget))
		}
	case api.PlatformTenantPlanResponse:
		_, _ = fmt.Fprintf(tw, "Plan\t%s (%s)\nPrice\t%s\nIncluded per month\t%d\nWeighted routes\t%d\nRequest budget\t%s\n", v.Name, v.ID,
			formatTenantPlanPrice(v), v.IncludedUnitsPerMonth, len(v.RouteWeights), formatPlanBudget(v.RequestBudget))
	case api.PlatformTenantPlanAssignmentResponse:
		_, _ = fmt.Fprintf(tw, "Plan\t%s\nRate card\t%s\nEffective from\t%s\n", dashIfEmpty(v.PlanName), v.RateCardID, v.EffectiveFrom.Format(time.RFC3339))
	case api.PlatformTenantPlanAssignmentListResponse:
		_, _ = fmt.Fprintln(tw, "EFFECTIVE FROM\tPLAN\tRATE CARD")
		for _, a := range v.Assignments {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", a.EffectiveFrom.Format(time.RFC3339), dashIfEmpty(a.PlanName), a.RateCardID)
		}
	}
	_ = tw.Flush()
}

func formatTenantPlanPrice(p api.PlatformTenantPlanResponse) string {
	return formatRateCardPrice(api.APIConsumerRateCardResponse{Currency: p.Currency, PriceMillicentsPerUnit: p.PriceMillicentsPerUnit, Tiers: p.Tiers})
}

func formatPlanBudget(b *api.PlatformTenantPlanBudget) string {
	if b == nil {
		return "-"
	}
	return fmt.Sprintf("%d/%d", b.MaxRequestsPerMinute, b.MaxRequestsPerDay)
}
