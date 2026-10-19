package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildConsumerCreditRequest validates the credit and spend cap verbs of
// `gregale consumers` (ADR-1306); handled is false for every other verb.
func buildConsumerCreditRequest(verb string, f consumerFlags) (any, bool, error) {
	switch verb {
	case "credit-grant":
		if strings.TrimSpace(f.currency) == "" || f.amountMillicents <= 0 {
			return nil, true, errors.New("--currency and a positive --amount-millicents are required")
		}
		req := api.CreateAPIConsumerCreditRequest{Currency: strings.ToUpper(strings.TrimSpace(f.currency)),
			AmountMillicents: f.amountMillicents, Note: f.note}
		if f.expires != "" {
			at, err := time.Parse(time.RFC3339, f.expires)
			if err != nil {
				return nil, true, fmt.Errorf("--expires: %w", err)
			}
			req.ExpiresAt = &at
		}
		return req, true, nil
	case "spend-cap-set":
		if strings.TrimSpace(f.currency) == "" || f.monthlyCapMillicents < 0 {
			return nil, true, errors.New("--currency and --monthly-cap-millicents (0 allows prepaid usage only) are required")
		}
		capMillicents := f.monthlyCapMillicents
		return api.PutAPIConsumerSpendCapRequest{Currency: strings.ToUpper(strings.TrimSpace(f.currency)), MonthlyCapMillicents: &capMillicents}, true, nil
	}
	return nil, false, nil
}

// callConsumerCredits calls the credit and spend cap endpoints; handled is
// false for every other verb.
func callConsumerCredits(ctx context.Context, client *Client, verb string, args []string, request any) (any, bool, error) {
	var out any
	var err error
	switch verb {
	case "credits":
		out, err = client.ListAPIConsumerCredits(ctx, args[0], args[1])
	case "credit-grant":
		out, err = client.CreateAPIConsumerCredit(ctx, args[0], args[1], request.(api.CreateAPIConsumerCreditRequest))
	case "credit-void":
		out, err = client.VoidAPIConsumerCredit(ctx, args[0], args[1], args[2])
	case "spend-cap":
		out, err = client.GetAPIConsumerSpendCap(ctx, args[0], args[1])
	case "spend-cap-set":
		out, err = client.PutAPIConsumerSpendCap(ctx, args[0], args[1], request.(api.PutAPIConsumerSpendCapRequest))
	case "spend-cap-delete":
		if err = client.DeleteAPIConsumerSpendCap(ctx, args[0], args[1]); err == nil {
			out = map[string]string{"status": "spend cap removed"}
		}
	default:
		return nil, false, nil
	}
	return out, true, err
}

func printConsumerCreditResult(tw *tabwriter.Writer, out any) bool {
	switch v := out.(type) {
	case api.APIConsumerCreditListResponse:
		for _, b := range v.Balances {
			_, _ = fmt.Fprintf(tw, "Available\t%s\n", formatMillicents(b.Currency, b.AvailableMillicents))
		}
		_, _ = fmt.Fprintln(tw, "ID\tSTATUS\tGRANTED\tREMAINING\tEXPIRES\tNOTE")
		for _, c := range v.Credits {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, c.Status, formatMillicents(c.Currency, c.AmountMillicents),
				formatMillicents(c.Currency, c.RemainingMillicents), formatDate(c.ExpiresAt), dashIfEmpty(c.Note))
		}
	case api.APIConsumerCreditResponse:
		_, _ = fmt.Fprintf(tw, "Credit\t%s\nStatus\t%s\nGranted\t%s\nRemaining\t%s\nExpires\t%s\n", v.ID, v.Status,
			formatMillicents(v.Currency, v.AmountMillicents), formatMillicents(v.Currency, v.RemainingMillicents), formatDate(v.ExpiresAt))
	case api.APIConsumerSpendCapResponse:
		state := "not reached"
		if v.Capped {
			state = "reached: requests are refused with 402 until the month ends or credit is added"
		}
		_, _ = fmt.Fprintf(tw, "Monthly cap\t%s\nCap\t%s\n", formatMillicents(v.Currency, v.MonthlyCapMillicents), state)
		if v.AmountDueMillicents != nil {
			_, _ = fmt.Fprintf(tw, "Due in %s\t%s\t(after prepaid credit, checked %s)\n", v.Month,
				formatMillicents(v.Currency, *v.AmountDueMillicents), v.CheckedAt.Format(time.RFC3339))
		}
	case map[string]string:
		_, _ = fmt.Fprintln(tw, v["status"])
	default:
		return false
	}
	return true
}
