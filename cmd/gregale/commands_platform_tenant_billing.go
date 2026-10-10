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
	"rate-cards":         {"id"},
	"rate-card-create":   {"id", "currency", "price-millicents", "included-units", "tier", "effective-from"},
	"statements":         {"id", "month", "period-start", "period-end"},
	"statement-draft":    {"id", "month", "period-start", "period-end"},
	"statement-show":     {"id", "statement-id"},
	"statement-finalize": {"id", "statement-id"},
	"statement-handoff":  {"id", "statement-id", "invoice-id"},
}

type platformTenantBillingFlags struct {
	id, statementID, invoiceID    string
	currency, effectiveFrom       string
	priceMillicents               int64
	includedUnits                 int64
	tiers                         multiFlag
	month, periodStart, periodEnd string
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
	fs.StringVar(&f.currency, "currency", "", "ISO-4217 currency, e.g. EUR")
	fs.Int64Var(&f.priceMillicents, "price-millicents", -1, "price per request in millicents; 100000 = 1.00")
	fs.Int64Var(&f.includedUnits, "included-units", 0, "free requests per tenant per UTC calendar month across all apps")
	fs.Var(&f.tiers, "tier", "graduated step UP_TO:PRICE_MILLICENTS counted per tenant per UTC month, repeatable; the last step's UP_TO is inf")
	fs.StringVar(&f.effectiveFrom, "effective-from", "", "UTC minute the price starts, RFC3339 (default: next minute)")
	fs.StringVar(&f.month, "month", "", "statement calendar month YYYY-MM")
	fs.StringVar(&f.periodStart, "period-start", "", "statement period start, RFC3339 UTC minute")
	fs.StringVar(&f.periodEnd, "period-end", "", "statement period end (exclusive), RFC3339 UTC minute")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, 0
		}
		return true, 1
	}
	if fs.NArg() != 0 || !flagsWithin(fs, allowed) || f.id == "" || (strings.HasPrefix(verb, "statement-") && verb != "statement-draft" && f.statementID == "") {
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
	case "statements", "statement-draft":
		start, end, err = statementPeriod(f.periodStart, f.periodEnd, f.month)
	case "statement-handoff":
		if strings.TrimSpace(f.invoiceID) == "" {
			err = errors.New("--invoice-id is required")
		}
	}
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
			IncludedUnitsPerMonth: rateCard.IncludedUnitsPerMonth, Tiers: rateCard.Tiers,
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
	case api.PlatformTenantStatementHandoffResponse:
		_, _ = fmt.Fprintf(tw, "Statement\t%s\nInvoice\t%s\nAmount\t%s\n", v.StatementID, v.ExternalInvoiceID, formatMillicents(v.Currency, v.AmountMillicents))
	default:
		return writeJSONTo(tw, out)
	}
	return tw.Flush()
}
