package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

var ErrProductBillingReviewChanged = errors.New("state: product billing review changed")
var ErrProductBillingUnresolved = errors.New("state: product billing has unresolved receipts")
var ErrProductBillingOpenPeriod = errors.New("state: product billing period is still open")

type ProductBillingScope struct{ AccountID, AppID, TenantID, Period string }

type ProductBillingStore interface {
	ReviewProductBilling(context.Context, ProductBillingScope) (api.ProductBillingReview, error)
	ListProductBillingReceipts(context.Context, ProductBillingScope, string) (api.ProductBillingReceipts, error)
	ListProductBillingDocuments(context.Context, ProductBillingScope, string) (api.ProductBillingDocuments, error)
	GetProductBillingDocument(context.Context, ProductBillingScope, string) (api.ProductBillingDocument, error)
	FinalizeProductBilling(context.Context, ProductBillingScope, string) (api.ProductBillingDocument, error)
	WaiveProductReceipt(context.Context, ProductBillingScope, string, string) (api.ProductReceiptEvidence, error)
	CreditProductReceipt(context.Context, ProductBillingScope, api.ProductCreditRequest) (api.ProductBillingDocument, error)
	HandoffProductBilling(context.Context, ProductBillingScope, string, string) (api.ProductInvoiceHandoff, error)
	GetProductBillingHandoff(context.Context, ProductBillingScope, string) (api.ProductInvoiceHandoff, error)
}

func validProductUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validateProductBillingScope(scope ProductBillingScope) error {
	account, accountErr := uuid.Parse(scope.AccountID)
	app, appErr := uuid.Parse(scope.AppID)
	// MemStore's existing account/app IDs use compact UUID spelling.
	if accountErr != nil || account == uuid.Nil || appErr != nil || app == uuid.Nil || !validProductUUID(scope.TenantID) || productcontrol.ValidatePeriod(scope.Period) != nil {
		return ErrInvalidArgument
	}
	return nil
}

func validateProductBillingCursor(cursor string) error {
	if cursor != "" && !validProductUUID(cursor) {
		return ErrInvalidArgument
	}
	return nil
}

func validProductBillingText(value string, max int) bool {
	return utf8.ValidString(value) && len(value) > 0 && len(value) <= max && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func productBillingReview(scope ProductBillingScope, currency string, lines []api.ProductBillingLine, now time.Time) (api.ProductBillingReview, error) {
	if len(lines) > api.ProductControlBillingMaxLines {
		return api.ProductBillingReview{}, NewOperationLimitError("product_billing_lines", api.ProductControlBillingMaxLines, int64(len(lines)))
	}
	start, _ := time.Parse("2006-01", scope.Period)
	review := api.ProductBillingReview{TenantID: scope.TenantID, Period: scope.Period, Currency: currency, Closed: !now.UTC().Before(start.AddDate(0, 1, 0)), Lines: lines}
	for _, line := range lines {
		if line.AmountMillicents < 0 || review.AmountMillicents > math.MaxInt64-line.AmountMillicents || review.Starts > math.MaxInt64-line.Starts {
			return review, ErrConflict
		}
		review.Starts += line.Starts
		review.Unresolved += line.Unresolved
		review.AmountMillicents += line.AmountMillicents
	}
	// Bind the review to its owner and application as well as its public evidence.
	raw, err := json.Marshal(struct {
		Account, App string
		Review       api.ProductBillingReview
	}{scope.AccountID, scope.AppID, review})
	if err != nil {
		return review, err
	}
	hash := sha256.Sum256(raw)
	review.ReviewHash = hex.EncodeToString(hash[:])
	return review, nil
}

func validateProductFinalization(review api.ProductBillingReview, expected string) error {
	if len(expected) != sha256.Size*2 || expected != review.ReviewHash {
		return ErrProductBillingReviewChanged
	}
	if !review.Closed {
		return ErrProductBillingOpenPeriod
	}
	if review.Unresolved != 0 {
		return ErrProductBillingUnresolved
	}
	return nil
}

func productStatement(review api.ProductBillingReview, now time.Time) api.ProductBillingDocument {
	return api.ProductBillingDocument{ID: uuid.NewString(), Kind: "statement", TenantID: review.TenantID, Period: review.Period, Currency: review.Currency, AmountMillicents: review.AmountMillicents, CreatedAt: now.Truncate(time.Microsecond), Snapshot: &review}
}

func validateProductCredit(req api.ProductCreditRequest) error {
	if !validProductUUID(req.ID) || !validProductUUID(req.OperationID) || req.AmountMillicents <= 0 || req.AmountMillicents > api.ProductControlMaxUnitPriceMillicents || !validProductBillingText(req.Reason, api.ProductControlBillingReasonMaxBytes) {
		return ErrInvalidArgument
	}
	return nil
}

func productCreditMatches(doc api.ProductBillingDocument, scope ProductBillingScope, req api.ProductCreditRequest) bool {
	return doc.Kind == "credit_note" && doc.TenantID == scope.TenantID && doc.Period == scope.Period && doc.OperationID == req.OperationID && doc.AmountMillicents == -req.AmountMillicents && doc.Reason == req.Reason
}

func newProductCredit(statement api.ProductBillingDocument, req api.ProductCreditRequest, now time.Time) api.ProductBillingDocument {
	return api.ProductBillingDocument{ID: req.ID, Kind: "credit_note", TenantID: statement.TenantID, Period: statement.Period, Currency: statement.Currency, StatementID: statement.ID, OperationID: req.OperationID, AmountMillicents: -req.AmountMillicents, Reason: req.Reason, CreatedAt: now.Truncate(time.Microsecond)}
}

func cloneProductBilling[T any](value T) T {
	// Only package-owned, JSON-compatible billing projections reach this helper.
	raw, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}
