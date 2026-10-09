package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// consumerVerbPositionals is the dispatcher's source of truth for the
// `gregale consumers` verb set and each verb's positional arguments.
var consumerVerbPositionals = map[string][]string{
	"list":               {"slug"},
	"create":             {"slug"},
	"info":               {"slug", "consumer-id"},
	"revoke":             {"slug", "consumer-id"},
	"keys":               {"slug", "consumer-id"},
	"key-create":         {"slug", "consumer-id"},
	"key-revoke":         {"slug", "consumer-id", "key-id"},
	"usage":              {"slug", "consumer-id"},
	"quote":              {"slug", "consumer-id"},
	"rate-cards":         {"slug"},
	"rate-card-create":   {"slug"},
	"statements":         {"slug", "consumer-id"},
	"statement-draft":    {"slug", "consumer-id"},
	"statement-show":     {"slug", "consumer-id", "statement-id"},
	"statement-finalize": {"slug", "consumer-id", "statement-id"},
	"statement-handoff":  {"slug", "consumer-id", "statement-id"},
}

const consumersUsage = "usage: gregale consumers <list|create|info|revoke|keys|key-create|key-revoke|usage|quote|rate-cards|rate-card-create|statements|statement-draft|statement-show|statement-finalize|statement-handoff> <slug> [consumer-id] [id] [flags]"

// consumerFlags holds every leaf flag; consumerVerbFlags decides which
// verb may set which, so a misplaced flag is a usage error, not ignored.
type consumerFlags struct {
	externalRef, name, scopes, expires string
	since, until                       string
	currency, effectiveFrom            string
	priceMillicents, includedUnits     int64
	periodStart, periodEnd, month      string
	invoiceID                          string
}

var consumerVerbFlags = map[string][]string{
	"create":            {"external-ref", "name"},
	"key-create":        {"name", "scopes", "expires"},
	"usage":             {"since", "until"},
	"quote":             {"since", "until"},
	"rate-card-create":  {"currency", "price-millicents", "included-units", "effective-from"},
	"statement-draft":   {"period-start", "period-end", "month"},
	"statement-handoff": {"invoice-id"},
}

// cmdConsumers manages an app's API consumers and their monetization:
// identities, keys, usage, rate cards, and usage statements.
func cmdConsumers(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, consumersUsage, "consumers")
		return 1
	}
	verb := args[0]
	positionals, ok := consumerVerbPositionals[verb]
	if !ok {
		PrintUsage(osStderr, consumersUsage, "consumers")
		return 1
	}
	fs := newFlagSet("consumers "+verb, flag.ContinueOnError)
	var f consumerFlags
	fs.StringVar(&f.externalRef, "external-ref", "", "stable consumer reference (create)")
	fs.StringVar(&f.name, "name", "", "consumer or key display name")
	fs.StringVar(&f.scopes, "scopes", "write", "comma-separated key scopes: read, write, admin (key-create)")
	fs.StringVar(&f.expires, "expires", "", "key expiry, RFC3339 (key-create)")
	fs.StringVar(&f.since, "since", "", "usage window start (RFC3339)")
	fs.StringVar(&f.until, "until", "", "usage window end (RFC3339)")
	fs.StringVar(&f.currency, "currency", "", "ISO-4217 currency, e.g. EUR (rate-card-create)")
	fs.Int64Var(&f.priceMillicents, "price-millicents", -1, "price per request in millicents; 100000 = 1.00 (rate-card-create)")
	fs.Int64Var(&f.includedUnits, "included-units", 0, "free requests per consumer per UTC calendar month (rate-card-create)")
	fs.StringVar(&f.effectiveFrom, "effective-from", "", "UTC minute the price starts, RFC3339 (default: next minute)")
	fs.StringVar(&f.periodStart, "period-start", "", "statement period start, RFC3339 UTC minute")
	fs.StringVar(&f.periodEnd, "period-end", "", "statement period end (exclusive), RFC3339 UTC minute")
	fs.StringVar(&f.month, "month", "", "statement calendar month YYYY-MM (instead of --period-start/--period-end)")
	fs.StringVar(&f.invoiceID, "invoice-id", "", "your billing system's invoice reference (statement-handoff)")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != len(positionals) || !flagsWithin(fs, consumerVerbFlags[verb]) {
		PrintUsage(osStderr, consumerVerbUsage(verb, positionals), "consumers")
		return 1
	}
	return runConsumers(verb, fs.Args(), f)
}

func consumerVerbUsage(verb string, positionals []string) string {
	usage := "usage: gregale consumers " + verb
	for _, p := range positionals {
		usage += " <" + p + ">"
	}
	for _, name := range consumerVerbFlags[verb] {
		usage += " [--" + name + "]"
	}
	return usage
}

func runConsumers(verb string, args []string, f consumerFlags) int {
	request, err := buildConsumerRequest(verb, f)
	if err != nil {
		return printErr("Invalid consumers arguments", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := callConsumers(context.Background(), client, verb, args, f, request)
	if err != nil {
		return printErr("Consumers "+verb+" failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if err := printConsumersResult(osStdout, out); err != nil {
		return printErr("Could not print result", err)
	}
	return 0
}

// buildConsumerRequest validates flags and builds the request body before
// authentication, so argument mistakes never reach the API.
func buildConsumerRequest(verb string, f consumerFlags) (any, error) {
	switch verb {
	case "create":
		if strings.TrimSpace(f.externalRef) == "" || strings.TrimSpace(f.name) == "" {
			return nil, errors.New("--external-ref and --name are required")
		}
		return api.CreateAPIConsumerRequest{ExternalRef: f.externalRef, Name: f.name}, nil
	case "key-create":
		return buildConsumerKeyRequest(f)
	case "rate-card-create":
		req, err := buildRateCardRequest(f.currency, f.priceMillicents, f.effectiveFrom)
		if f.includedUnits < 0 {
			return nil, errors.New("--included-units must be non-negative")
		}
		req.IncludedUnitsPerMonth = f.includedUnits
		return req, err
	case "statement-draft":
		start, end, err := statementPeriod(f.periodStart, f.periodEnd, f.month)
		if err != nil {
			return nil, err
		}
		return api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}, nil
	case "statement-handoff":
		if strings.TrimSpace(f.invoiceID) == "" {
			return nil, errors.New("--invoice-id is required")
		}
		return api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: f.invoiceID}, nil
	}
	return nil, nil
}

func buildConsumerKeyRequest(f consumerFlags) (api.CreateConsumerKeyRequest, error) {
	req := api.CreateConsumerKeyRequest{Name: f.name}
	if strings.TrimSpace(f.name) == "" {
		return req, errors.New("--name is required")
	}
	for _, scope := range strings.Split(f.scopes, ",") {
		scope = strings.TrimSpace(scope)
		if scope != "read" && scope != "write" && scope != "admin" {
			return req, fmt.Errorf("unknown scope %q; use read, write, or admin", scope)
		}
		req.Scopes = append(req.Scopes, scope)
	}
	if f.expires != "" {
		at, err := time.Parse(time.RFC3339, f.expires)
		if err != nil {
			return req, fmt.Errorf("--expires: %w", err)
		}
		req.ExpiresAt = &at
	}
	return req, nil
}

// buildRateCardRequest is shared by app and platform-tenant rate cards,
// whose request bodies have the same shape.
func buildRateCardRequest(currency string, priceMillicents int64, effectiveFrom string) (api.CreateAPIConsumerRateCardRequest, error) {
	req := api.CreateAPIConsumerRateCardRequest{Currency: strings.ToUpper(strings.TrimSpace(currency)), PriceMillicentsPerUnit: priceMillicents}
	if len(req.Currency) != 3 {
		return req, errors.New("--currency must be a three-letter ISO-4217 code")
	}
	if priceMillicents < 0 {
		return req, errors.New("--price-millicents is required and must be non-negative")
	}
	if effectiveFrom != "" {
		at, err := time.Parse(time.RFC3339, effectiveFrom)
		if err != nil {
			return req, fmt.Errorf("--effective-from: %w", err)
		}
		at = at.UTC()
		if !at.Equal(at.Truncate(time.Minute)) {
			return req, errors.New("--effective-from must be a whole UTC minute")
		}
		req.EffectiveFrom = &at
	}
	return req, nil
}

// statementPeriod resolves either an explicit UTC-minute period or a
// calendar month into the [start, end) window statements are keyed by.
func statementPeriod(startText, endText, month string) (time.Time, time.Time, error) {
	if month != "" {
		if startText != "" || endText != "" {
			return time.Time{}, time.Time{}, errors.New("use --month or --period-start/--period-end, not both")
		}
		start, err := time.Parse("2006-01", month)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--month must be YYYY-MM: %w", err)
		}
		return start.UTC(), start.UTC().AddDate(0, 1, 0), nil
	}
	if startText == "" || endText == "" {
		return time.Time{}, time.Time{}, errors.New("--month or both --period-start and --period-end are required")
	}
	start, err := time.Parse(time.RFC3339, startText)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("--period-start: %w", err)
	}
	end, err := time.Parse(time.RFC3339, endText)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("--period-end: %w", err)
	}
	start, end = start.UTC(), end.UTC()
	if !start.Equal(start.Truncate(time.Minute)) || !end.Equal(end.Truncate(time.Minute)) || !end.After(start) {
		return time.Time{}, time.Time{}, errors.New("the period must be whole UTC minutes with --period-end after --period-start")
	}
	return start, end, nil
}

func callConsumers(ctx context.Context, client *Client, verb string, args []string, f consumerFlags, request any) (any, error) {
	slug := args[0]
	switch verb {
	case "list":
		return client.ListAPIConsumers(ctx, slug)
	case "create":
		return client.CreateAPIConsumer(ctx, slug, request.(api.CreateAPIConsumerRequest))
	case "info":
		return client.GetAPIConsumer(ctx, slug, args[1])
	case "revoke":
		return client.RevokeAPIConsumer(ctx, slug, args[1])
	case "keys":
		return client.ListConsumerKeys(ctx, slug, args[1])
	case "key-create":
		return client.CreateConsumerKey(ctx, slug, args[1], request.(api.CreateConsumerKeyRequest))
	case "key-revoke":
		return client.RevokeConsumerKey(ctx, slug, args[1], args[2])
	case "usage":
		return client.GetAPIConsumerUsage(ctx, slug, args[1], api.APIConsumerUsageOptions{Since: f.since, Until: f.until})
	case "quote":
		return client.GetAPIConsumerUsageQuote(ctx, slug, args[1], api.APIConsumerUsageOptions{Since: f.since, Until: f.until})
	case "rate-cards":
		return client.ListAPIConsumerRateCards(ctx, slug)
	case "rate-card-create":
		return client.CreateAPIConsumerRateCard(ctx, slug, request.(api.CreateAPIConsumerRateCardRequest))
	}
	return callConsumerStatements(ctx, client, verb, args, request)
}

func callConsumerStatements(ctx context.Context, client *Client, verb string, args []string, request any) (any, error) {
	slug, consumerID := args[0], args[1]
	switch verb {
	case "statements":
		return client.ListAPIConsumerUsageStatements(ctx, slug, consumerID)
	case "statement-draft":
		return client.CreateAPIConsumerUsageStatement(ctx, slug, consumerID, request.(api.CreateAPIConsumerUsageStatementRequest))
	case "statement-show":
		return client.GetAPIConsumerUsageStatement(ctx, slug, consumerID, args[2])
	case "statement-finalize":
		return client.FinalizeAPIConsumerUsageStatement(ctx, slug, consumerID, args[2])
	case "statement-handoff":
		return client.ClaimAPIConsumerUsageStatement(ctx, slug, consumerID, args[2], request.(api.ClaimAPIConsumerUsageStatementRequest))
	}
	return nil, fmt.Errorf("unknown consumers verb %q", verb)
}

// formatMillicents renders an exact amount (100,000 millicents per currency
// unit) without floats, keeping sub-cent precision when present.
func formatMillicents(currency string, millicents int64) string {
	if currency == "" {
		currency = "---"
	}
	sign := ""
	if millicents < 0 {
		sign, millicents = "-", -millicents
	}
	fraction := strings.TrimRight(fmt.Sprintf("%05d", millicents%100000), "0")
	for len(fraction) < 2 {
		fraction += "0"
	}
	return fmt.Sprintf("%s %s%d.%s", currency, sign, millicents/100000, fraction)
}

func statementRevisionLabel(revision int, status string) string {
	return fmt.Sprintf("r%d %s", revision, status)
}

func printConsumersResult(w io.Writer, out any) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	switch v := out.(type) {
	case api.APIConsumerListResponse:
		_, _ = fmt.Fprintln(tw, "ID\tEXTERNAL REF\tNAME\tSTATUS")
		for _, c := range v.Consumers {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.ID, c.ExternalRef, c.Name, c.Status)
		}
	case api.APIConsumerResponse:
		_, _ = fmt.Fprintf(tw, "Consumer\t%s\nExternal ref\t%s\nName\t%s\nStatus\t%s\n", v.ID, v.ExternalRef, v.Name, v.Status)
	case api.ConsumerKeyListResponse:
		_, _ = fmt.Fprintln(tw, "ID\tNAME\tPREFIX\tSCOPES\tSTATUS")
		for _, k := range v.Keys {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", k.ID, k.Name, k.Prefix, strings.Join(k.Scopes, ","), consumerKeyStatus(k))
		}
	case api.ConsumerKeyResponse:
		_, _ = fmt.Fprintf(tw, "Key\t%s\nName\t%s\nScopes\t%s\nStatus\t%s\n", v.ID, v.Name, strings.Join(v.Scopes, ","), consumerKeyStatus(v))
		if v.Key != "" {
			_, _ = fmt.Fprintf(tw, "Secret\t%s\t(shown once; store it now)\n", v.Key)
		}
	default:
		return printConsumerBillingResult(tw, out)
	}
	return tw.Flush()
}

func consumerKeyStatus(k api.ConsumerKeyResponse) string {
	switch {
	case k.RevokedAt != nil:
		return "revoked"
	case k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now()):
		return "expired"
	}
	return "active"
}

func printConsumerBillingResult(tw *tabwriter.Writer, out any) error {
	switch v := out.(type) {
	case api.APIConsumerUsageResponse:
		_, _ = fmt.Fprintf(tw, "Window\t%s – %s\nRequests\t%d\nErrors\t%d\nBillable units\t%d\n",
			v.PeriodStart.Format(time.RFC3339), v.PeriodEnd.Format(time.RFC3339), v.RequestCount, v.ErrorCount, v.BillableUnits)
	case api.APIConsumerUsageQuoteResponse:
		_, _ = fmt.Fprintf(tw, "Window\t%s – %s\nBillable units\t%d\nUnpriced units\t%d\nEstimate\t%s\n",
			v.PeriodStart.Format(time.RFC3339), v.PeriodEnd.Format(time.RFC3339), v.BillableUnits, v.UnpricedUnits, formatMillicents(v.Currency, v.AmountMillicents))
	case api.APIConsumerRateCardListResponse:
		_, _ = fmt.Fprintln(tw, "ID\tEFFECTIVE FROM\tPRICE PER REQUEST\tINCLUDED PER MONTH")
		for _, c := range v.RateCards {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", c.ID, c.EffectiveFrom.Format(time.RFC3339), formatMillicents(c.Currency, c.PriceMillicentsPerUnit), c.IncludedUnitsPerMonth)
		}
	case api.APIConsumerRateCardResponse:
		_, _ = fmt.Fprintf(tw, "Rate card\t%s\nEffective from\t%s\nPrice per request\t%s\nIncluded per month\t%d requests per consumer\n",
			v.ID, v.EffectiveFrom.Format(time.RFC3339), formatMillicents(v.Currency, v.PriceMillicentsPerUnit), v.IncludedUnitsPerMonth)
	case api.APIConsumerUsageStatementListResponse:
		_, _ = fmt.Fprintln(tw, "ID\tPERIOD START\tPERIOD END\tREVISION\tUNITS\tAMOUNT")
		for _, s := range v.Statements {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", s.ID, s.PeriodStart.Format(time.RFC3339), s.PeriodEnd.Format(time.RFC3339),
				statementRevisionLabel(s.Revision, s.Status), s.BillableUnits, formatMillicents(s.Currency, s.AmountMillicents))
		}
	case api.APIConsumerUsageStatementResponse:
		printStatementSummary(tw, v.ID, v.PeriodStart, v.PeriodEnd, v.Revision, v.Status, v.Currency, v.BillableUnits, v.UnpricedUnits, v.AmountMillicents)
		var charged int64
		for _, bucket := range v.Buckets {
			charged += bucket.ChargedUnits
		}
		if priced := v.BillableUnits - v.UnpricedUnits; charged != priced {
			_, _ = fmt.Fprintf(tw, "Charged units\t%d\t(the monthly allowance covers the rest; an adjustment can charge units that were free before)\n", charged)
		}
	case api.APIConsumerUsageStatementHandoffResponse:
		_, _ = fmt.Fprintf(tw, "Statement\t%s\nInvoice\t%s\nAmount\t%s\n", v.StatementID, v.ExternalInvoiceID, formatMillicents(v.Currency, v.AmountMillicents))
	default:
		if err := writeJSONTo(tw, out); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// printStatementSummary renders the fields app and tenant statements share,
// and says what a revision means so an adjustment is not billed as a total.
func printStatementSummary(tw *tabwriter.Writer, id string, start, end time.Time, revision int, status, currency string, units, unpriced, amount int64) {
	_, _ = fmt.Fprintf(tw, "Statement\t%s\nPeriod\t%s – %s\nRevision\t%s\nBillable units\t%d\nAmount\t%s\n",
		id, start.Format(time.RFC3339), end.Format(time.RFC3339), statementRevisionLabel(revision, status), units, formatMillicents(currency, amount))
	if unpriced > 0 {
		_, _ = fmt.Fprintf(tw, "Unpriced units\t%d\t(add a rate card, then draft again before finalizing)\n", unpriced)
	}
	if revision > 1 && status != "superseded" {
		_, _ = fmt.Fprintf(tw, "Note\tthis revision bills only units not covered by earlier finalized revisions\n")
	}
}
