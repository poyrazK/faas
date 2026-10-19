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

// platformTenantBillingVerbs are the cross-app pricing and statement verbs of
// `gregale platform-tenants`, mapped to the flags each accepts.
var platformTenantBillingVerbs = map[string][]string{
	"rate-cards": {"id"},
	"rate-card-create": {"id", "currency", "price-millicents", "included-units", "tier", "weight", "effective-from", "unit", "tier-mode",
		"base-fee-millicents", "minimum-millicents", "prorate-monthly-fees"},
	"statements":         {"id", "month", "period-start", "period-end"},
	"statement-draft":    {"id", "month", "period-start", "period-end"},
	"statement-show":     {"id", "statement-id"},
	"statement-finalize": {"id", "statement-id"},
	"statement-handoff":  {"id", "statement-id", "invoice-id"},
	"statement-export":   {"id", "statement-id", "customer", "provider"},
	// Trials (ADR-1322) and the account's overdue rule (ADR-1324).
	"trial":                 {"id"},
	"trial-set":             {"id", "days", "ends-at"},
	"trial-end":             {"id"},
	"billing-policy":        {},
	"billing-policy-set":    {"block-overdue-after-days"},
	"billing-policy-delete": {},
	"overdue-holds":         {},
}

// accountTenantVerbs address the account's tenants as a whole, not one
// tenant.
var accountTenantVerbs = map[string]bool{"billing-policy": true, "billing-policy-set": true, "billing-policy-delete": true, "overdue-holds": true}

type platformTenantBillingFlags struct {
	id, statementID, invoiceID    string
	customer, provider            string
	currency, effectiveFrom       string
	unit, tierMode                string
	priceMillicents               int64
	includedUnits                 int64
	tiers, weights                multiFlag
	month, periodStart, periodEnd string
	baseFee, minimum              int64
	prorate                       bool
	days, blockAfterDays          int
	endsAt                        string
}

// cmdPlatformTenantBilling handles the billing verbs; handled is false for
// every other platform-tenants verb.
func cmdPlatformTenantBilling(verb string, args []string) (handled bool, code int) {
	allowed, ok := platformTenantBillingVerbs[verb]
	if !ok {
		return false, 0
	}
	fs := newFlagSet("platform-tenants "+verb, flag.ContinueOnError)
	var f platformTenantBillingFlags
	fs.StringVar(&f.id, "id", "", "platform tenant UUID")
	fs.StringVar(&f.statementID, "statement-id", "", "statement revision UUID")
	fs.StringVar(&f.invoiceID, "invoice-id", "", "your billing system's invoice reference")
	fs.StringVar(&f.customer, "customer", "", "provider customer: Stripe cus_…, Paddle ctm_… or Lago external ID (statement-export; default: the tenant's external_ref)")
	fs.StringVar(&f.provider, "provider", "", "billing provider: stripe, paddle or lago (statement-export; default stripe)")
	fs.StringVar(&f.currency, "currency", "", "ISO-4217 currency, e.g. EUR")
	fs.Int64Var(&f.priceMillicents, "price-millicents", -1, "price per request in millicents; 100000 = 1.00")
	fs.Int64Var(&f.includedUnits, "included-units", 0, "free requests per tenant per UTC calendar month across all apps")
	fs.Var(&f.tiers, "tier", "graduated step UP_TO:PRICE_MILLICENTS counted per tenant per UTC month, repeatable; the last step's UP_TO is inf")
	fs.Var(&f.weights, "weight", "route weight \"METHOD /template=N\", repeatable; unlisted routes count 1")
	fs.StringVar(&f.effectiveFrom, "effective-from", "", "UTC minute the price starts, RFC3339 (default: next minute)")
	fs.StringVar(&f.unit, "unit", "", "what the card prices: request (default), compute_second, response_megabyte or app:<meter>")
	fs.Int64Var(&f.baseFee, "base-fee-millicents", 0, "fixed charge per tenant per UTC month; 100000 = 1.00")
	fs.Int64Var(&f.minimum, "minimum-millicents", 0, "minimum usage charge per tenant per UTC month, topped up when usage costs less")
	fs.BoolVar(&f.prorate, "prorate-monthly-fees", false, "scale the fee and minimum in a tenant's first month to the share left after it was created")
	fs.IntVar(&f.days, "days", 0, "trial length in days from its start (trial-set)")
	fs.StringVar(&f.endsAt, "ends-at", "", "trial end, RFC3339 UTC minute (trial-set)")
	fs.IntVar(&f.blockAfterDays, "block-overdue-after-days", -1, "days past an invoice's due date before the tenant is refused (billing-policy-set)")
	fs.StringVar(&f.tierMode, "tier-mode", "", "how --tier steps price a month: graduated (default) or volume")
	fs.StringVar(&f.month, "month", "", "statement calendar month YYYY-MM")
	fs.StringVar(&f.periodStart, "period-start", "", "statement period start, RFC3339 UTC minute")
	fs.StringVar(&f.periodEnd, "period-end", "", "statement period end (exclusive), RFC3339 UTC minute")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, 0
		}
		return true, 1
	}
	if fs.NArg() != 0 || !flagsWithin(fs, allowed) || (f.id == "" && !accountTenantVerbs[verb]) ||
		(strings.HasPrefix(verb, "statement-") && verb != "statement-draft" && f.statementID == "") {
		PrintUsage(osStderr, platformTenantBillingUsage(verb, allowed), "platform-tenants")
		return true, 1
	}
	return true, runPlatformTenantBilling(verb, f)
}

func platformTenantBillingUsage(verb string, allowed []string) string {
	usage := "usage: gregale platform-tenants " + verb
	for _, name := range allowed {
		usage += " --" + name
	}
	return usage
}

// flagsWithin reports whether every flag the user set is in allowed.
func flagsWithin(fs *flag.FlagSet, allowed []string) bool {
	permitted := map[string]bool{}
	for _, name := range allowed {
		permitted[name] = true
	}
	ok := true
	fs.Visit(func(set *flag.Flag) {
		if !permitted[set.Name] {
			ok = false
		}
	})
	return ok
}

func runPlatformTenantBilling(verb string, f platformTenantBillingFlags) int {
	var rateCard api.CreateAPIConsumerRateCardRequest
	var start, end time.Time
	var err error
	switch verb {
	case "rate-card-create":
		rateCard, err = buildPricingRequest(consumerFlags{currency: f.currency, priceMillicents: f.priceMillicents,
			includedUnits: f.includedUnits, tiers: f.tiers, effectiveFrom: f.effectiveFrom})
		if err == nil {
			rateCard.RouteWeights, err = parseRouteWeights(f.weights)
		}
	case "statements", "statement-draft":
		start, end, err = statementPeriod(f.periodStart, f.periodEnd, f.month)
	case "statement-handoff":
		if strings.TrimSpace(f.invoiceID) == "" {
			err = errors.New("--invoice-id is required")
		}
	case "billing-policy-set":
		if f.blockAfterDays < 0 {
			err = errors.New("--block-overdue-after-days is required")
		}
	}
	if err != nil {
		return printErr("Invalid platform-tenants arguments", err)
	}
	trial, err := trialRequest(verb, f.days, f.endsAt)
	if err != nil {
		return printErr("Invalid platform-tenants arguments", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out any
	switch verb {
	case "rate-cards":
		out, err = client.ListPlatformTenantRateCards(ctx, f.id)
	case "rate-card-create":
		out, err = client.CreatePlatformTenantRateCard(ctx, f.id, api.CreatePlatformTenantRateCardRequest{
			Currency: rateCard.Currency, PriceMillicentsPerUnit: rateCard.PriceMillicentsPerUnit, EffectiveFrom: rateCard.EffectiveFrom,
			IncludedUnitsPerMonth: rateCard.IncludedUnitsPerMonth, Tiers: rateCard.Tiers, RouteWeights: rateCard.RouteWeights,
			Unit: strings.TrimSpace(f.unit), TierMode: strings.TrimSpace(f.tierMode),
			BaseFeeMillicentsPerMonth: f.baseFee, MinimumMillicentsPerMonth: f.minimum, ProrateMonthlyFees: f.prorate,
		})
	case "statements":
		out, err = client.ListPlatformTenantStatements(ctx, f.id, start, end)
	case "statement-draft":
		out, err = client.CreatePlatformTenantStatement(ctx, f.id, api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end})
	case "statement-show":
		out, err = client.GetPlatformTenantStatement(ctx, f.id, f.statementID)
	case "statement-finalize":
		out, err = client.FinalizePlatformTenantStatement(ctx, f.id, f.statementID)
	case "statement-handoff":
		out, err = client.ClaimPlatformTenantStatement(ctx, f.id, f.statementID, api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: f.invoiceID})
	case "statement-export":
		out, err = client.ExportPlatformTenantStatementInvoice(ctx, f.id, f.statementID, api.CreateStatementInvoiceRequest{Provider: strings.TrimSpace(f.provider), Customer: strings.TrimSpace(f.customer)})
	default:
		out, err = callTenantControls(ctx, client, verb, f, trial)
	}
	if err != nil {
		return printErr("Platform tenant "+verb+" failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if err := printPlatformTenantBilling(out); err != nil {
		return printErr("Could not print result", err)
	}
	return 0
}

// callTenantControls runs the trial and overdue verbs; a nil result with a
// nil error means the verb printed nothing to report.
func callTenantControls(ctx context.Context, client *Client, verb string, f platformTenantBillingFlags,
	trial api.PutAPIConsumerTrialRequest) (any, error) {
	switch verb {
	case "trial":
		return client.GetPlatformTenantTrial(ctx, f.id)
	case "trial-set":
		return client.PutPlatformTenantTrial(ctx, f.id, trial)
	case "trial-end":
		return map[string]string{"status": "ended"}, client.EndPlatformTenantTrial(ctx, f.id)
	case "billing-policy":
		return client.GetPlatformTenantBillingPolicy(ctx)
	case "billing-policy-set":
		days := f.blockAfterDays
		return client.PutPlatformTenantBillingPolicy(ctx, api.PutAPIConsumerBillingPolicyRequest{BlockOverdueAfterDays: &days})
	case "billing-policy-delete":
		return map[string]string{"status": "deleted"}, client.DeletePlatformTenantBillingPolicy(ctx)
	case "overdue-holds":
		return client.ListPlatformTenantOverdueHolds(ctx)
	}
	return nil, fmt.Errorf("unknown platform-tenants verb %q", verb)
}

// formatTenantRateCardPrice summarizes a tenant card's flat price or ladder.
func formatTenantRateCardPrice(c api.PlatformTenantRateCardResponse) string {
	return formatRateCardPrice(api.APIConsumerRateCardResponse{Currency: c.Currency, PriceMillicentsPerUnit: c.PriceMillicentsPerUnit, Tiers: c.Tiers})
}

func printPlatformTenantBilling(out any) error {
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	switch v := out.(type) {
	case api.PlatformTenantRateCardListResponse:
		_, _ = fmt.Fprintln(tw, "ID\tEFFECTIVE FROM\tPRICE PER REQUEST\tINCLUDED PER MONTH")
		for _, c := range v.RateCards {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", c.ID, c.EffectiveFrom.Format(time.RFC3339), formatTenantRateCardPrice(c), c.IncludedUnitsPerMonth)
		}
	case api.PlatformTenantRateCardResponse:
		_, _ = fmt.Fprintf(tw, "Rate card\t%s\nEffective from\t%s\nPrice per request\t%s\nIncluded per month\t%d requests across the tenant's apps\n",
			v.ID, v.EffectiveFrom.Format(time.RFC3339), formatTenantRateCardPrice(v), v.IncludedUnitsPerMonth)
	case api.PlatformTenantStatementListResponse:
		_, _ = fmt.Fprintln(tw, "ID\tREVISION\tUNITS\tAMOUNT")
		for _, s := range v.Statements {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", s.ID, statementRevisionLabel(s.Revision, s.Status), s.BillableUnits, formatMillicents(s.Currency, s.AmountMillicents))
		}
	case api.PlatformTenantStatementResponse:
		printStatementSummary(tw, v.ID, v.PeriodStart, v.PeriodEnd, v.Revision, v.Status, v.Currency, v.BillableUnits, v.UnpricedUnits, v.AmountMillicents)
	case api.StatementInvoiceExportResponse:
		printStatementInvoiceExport(tw, v)
	case api.PlatformTenantStatementHandoffResponse:
		_, _ = fmt.Fprintf(tw, "Statement\t%s\nInvoice\t%s\nAmount\t%s\n", v.StatementID, v.ExternalInvoiceID, formatMillicents(v.Currency, v.AmountMillicents))
	case api.PlatformTenantTrialResponse:
		_, _ = fmt.Fprintf(tw, "Tenant\t%s\nStarts\t%s\nEnds\t%s\nActive\t%t\n", v.TenantID,
			v.StartsAt.Format(time.RFC3339), v.EndsAt.Format(time.RFC3339), v.Active)
	case api.PlatformTenantBillingPolicyResponse:
		_, _ = fmt.Fprintf(tw, "Block overdue after\t%d days\nUpdated\t%s\n", v.BlockOverdueAfterDays, v.UpdatedAt.Format(time.RFC3339))
	case api.PlatformTenantOverdueHoldListResponse:
		_, _ = fmt.Fprintln(tw, "TENANT\tINVOICE EXPORT\tDUE\tBLOCKED SINCE")
		for _, h := range v.Holds {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", h.TenantID, h.ExportID, h.DueAt.Format(time.RFC3339), h.Since.Format(time.RFC3339))
		}
	default:
		return writeJSONTo(tw, out)
	}
	return tw.Flush()
}
