package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func productBillingFlags(fs *flag.FlagSet, c *productCommand) {
	fs.StringVar(&c.tenant, "tenant", "", "customer platform tenant UUID")
	fs.StringVar(&c.period, "period", productcontrol.Period(time.Now()), "UTC admission month YYYY-MM")
	switch c.verb {
	case "statements list", "statements review":
		fs.BoolVar(&c.self, "self", false, "use authenticated customer's billing view")
	}
	switch c.verb {
	case "statements list", "statements receipts":
		fs.StringVar(&c.cursor, "cursor", "", "next page cursor")
	case "statements finalize":
		fs.StringVar(&c.reviewHash, "review-hash", "", "review hash to confirm; preserve for retries")
	case "statements waive", "statements credit":
		fs.StringVar(&c.operation, "operation", "", "logical operation UUID")
		fs.StringVar(&c.reason, "reason", "", "permanent billing decision reason (no secrets)")
		if c.verb == "statements credit" {
			fs.StringVar(&c.billingID, "id", "", "stable credit note UUID; preserve for retries")
			fs.Int64Var(&c.amountMillicents, "amount-millicents", 0, "positive credit amount in millicents")
		}
	case "statements handoff", "statements handoff-status", "statements export", "statements export-status":
		fs.StringVar(&c.document, "document", "", "finalized statement or credit note UUID")
		if c.verb == "statements handoff" {
			fs.StringVar(&c.invoice, "invoice-id", "", "stable external invoice or credit note reference; preserve for retries")
		}
		if c.verb == "statements export" {
			fs.StringVar(&c.billingProvider, "provider", "", "account billing connection: stripe, paddle or lago")
			fs.StringVar(&c.billingCustomer, "customer", "", "provider customer ID (default the tenant's external reference)")
		}
	}
}

func validateProductBillingCommand(c productCommand) error {
	if productcontrol.ValidatePeriod(c.period) != nil {
		return errors.New("--period must be YYYY-MM")
	}
	validID := func(value string) bool {
		id, err := uuid.Parse(value)
		return err == nil && id != uuid.Nil && id.String() == value
	}
	if (!c.self && !validID(c.tenant)) || (c.self && c.tenant != "") {
		return errors.New("use either --tenant UUID or --self")
	}
	if c.cursor != "" && !validID(c.cursor) {
		return errors.New("--cursor must be a returned document or receipt UUID")
	}
	switch c.verb {
	case "statements finalize":
		if raw, err := hex.DecodeString(c.reviewHash); err != nil || len(raw) != 32 {
			return errors.New("--review-hash from statements review is required")
		}
	case "statements waive", "statements credit":
		if !validID(c.operation) || strings.TrimSpace(c.reason) == "" || len(c.reason) > api.ProductControlBillingReasonMaxBytes {
			return errors.New("--operation UUID and a reason of at most 512 bytes are required")
		}
		if c.verb == "statements credit" && (!validID(c.billingID) || c.amountMillicents <= 0 || c.amountMillicents > api.ProductControlMaxUnitPriceMillicents) {
			return errors.New("--id must be a stable credit UUID and --amount-millicents must fit the per-operation price limit")
		}
	case "statements handoff", "statements handoff-status", "statements export", "statements export-status":
		if !validID(c.document) {
			return errors.New("--document UUID is required")
		}
		if c.verb == "statements export" && c.billingProvider != "stripe" && c.billingProvider != "paddle" && c.billingProvider != "lago" {
			return errors.New("--provider must be stripe, paddle or lago")
		}
		if c.verb == "statements export" && (c.billingCustomer != strings.TrimSpace(c.billingCustomer) || len(c.billingCustomer) > api.ProductControlInvoiceReferenceMaxBytes) {
			return errors.New("--customer must be a provider customer ID of at most 255 bytes")
		}
		if c.verb == "statements handoff" && (strings.TrimSpace(c.invoice) == "" || len(c.invoice) > api.ProductControlInvoiceReferenceMaxBytes) {
			return errors.New("--invoice-id of at most 255 bytes is required")
		}
	}
	return nil
}

func runProductBillingCommand(ctx context.Context, client *Client, c productCommand) (any, error) {
	if c.self {
		view, err := client.GetProductCustomerBilling(ctx, c.app, c.period, c.cursor)
		if c.verb == "statements review" {
			return view.Review, err
		}
		return view.Documents, err
	}
	switch c.verb {
	case "statements review":
		return client.ReviewProductBilling(ctx, c.app, c.tenant, c.period)
	case "statements list":
		return client.ListProductBillingDocuments(ctx, c.app, c.tenant, c.period, c.cursor)
	case "statements receipts":
		return client.ListProductBillingReceipts(ctx, c.app, c.tenant, c.period, c.cursor)
	case "statements finalize":
		return client.FinalizeProductBilling(ctx, c.app, api.ProductFinalizeRequest{TenantID: c.tenant, Period: c.period, ExpectedHash: c.reviewHash})
	case "statements waive":
		return client.WaiveProductReceipt(ctx, c.app, c.period, c.operation, api.ProductWaiverRequest{TenantID: c.tenant, Reason: c.reason})
	case "statements credit":
		return client.CreditProductReceipt(ctx, c.app, c.period, api.ProductCreditRequest{ID: c.billingID, TenantID: c.tenant, OperationID: c.operation, AmountMillicents: c.amountMillicents, Reason: c.reason})
	case "statements handoff":
		return client.HandoffProductBilling(ctx, c.app, c.period, c.document, api.ProductHandoffRequest{TenantID: c.tenant, ExternalInvoiceID: c.invoice})
	case "statements handoff-status":
		return client.GetProductBillingHandoff(ctx, c.app, c.tenant, c.period, c.document)
	case "statements export":
		return client.ExportProductStatement(ctx, c.app, c.period, c.document, api.ProductInvoiceExportRequest{TenantID: c.tenant, Provider: c.billingProvider, Customer: c.billingCustomer})
	case "statements export-status":
		return client.GetProductStatementExport(ctx, c.app, c.tenant, c.period, c.document)
	}
	return nil, errors.New("unsupported billing command")
}

// productOverdueView is what billing overdue-policy inspect prints: the
// policy, null when the app has none, and the customers it holds.
type productOverdueView struct {
	Policy *api.ProductOverduePolicy `json:"policy"`
	Holds  []api.ProductOverdueHold  `json:"holds"`
}

// runProductOverdueCommand reads or changes the app's overdue policy
// (ADR-20261018212918).
func runProductOverdueCommand(ctx context.Context, client *Client, c productCommand) (any, error) {
	switch c.verb {
	case "billing overdue-policy set":
		return client.PutProductOverduePolicy(ctx, c.app, c.overdueDays)
	case "billing overdue-policy clear":
		if err := client.DeleteProductOverduePolicy(ctx, c.app); err != nil {
			return nil, err
		}
		return map[string]string{"status": "overdue policy cleared; its holds are lifted"}, nil
	}
	view := productOverdueView{Holds: []api.ProductOverdueHold{}}
	policy, err := client.GetProductOverduePolicy(ctx, c.app)
	var problem *api.APIError
	switch {
	case err == nil:
		view.Policy = &policy
	case errors.As(err, &problem) && problem.Problem.Status == http.StatusNotFound:
	default:
		return nil, err
	}
	holds, err := client.ListProductOverdueHolds(ctx, c.app)
	if err != nil {
		return nil, err
	}
	if holds.Holds != nil {
		view.Holds = holds.Holds
	}
	return view, nil
}
