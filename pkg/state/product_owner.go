package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

var ErrProductCustomerChangeStale = errors.New("state: Product Control customer change preview is stale")

// Kept separate from Store and the execution admission contract.
type ProductOwnerStore interface {
	PreviewProductCustomerChange(context.Context, string, string, api.ProductCustomer) (api.ProductCustomerChangePreview, error)
	ApplyProductCustomerChange(context.Context, string, string, api.ProductCustomerChangeRequest) (api.ProductCustomerChangeReceipt, error)
	ListProductOwnerRuns(context.Context, ProductBillingScope, string) (api.ProductOwnerRuns, error)
	ListProductOwnerAttention(context.Context, string, string, string) (api.ProductOwnerAttentionPage, error)
	InspectProductBinding(context.Context, string, string, api.ProductFeature) (OperationDefinition, error)
	GetProductOwnerDocument(context.Context, ProductBillingScope, string) (api.ProductBillingDocument, error)
}

func productOwnerHash(value any) string {
	raw, _ := json.Marshal(value) // Only package-owned JSON-compatible projections.
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func productChangePreview(account, app string, before *api.ProductCustomer, beforeCatalog *api.ProductCatalog, target api.ProductCustomer, catalog, head api.ProductCatalog, now time.Time) (api.ProductCustomerChangePreview, error) {
	if !validProductUUID(target.TenantID) || validateProductCustomerIdentity(target) != nil {
		return api.ProductCustomerChangePreview{}, ErrInvalidArgument
	}
	if _, ok := catalog.Spec.Plans[target.Plan]; !ok {
		return api.ProductCustomerChangePreview{}, ErrNotFound
	}
	if before != nil {
		if target.AuthOrganizationID != "" && target.AuthOrganizationID != before.AuthOrganizationID {
			return api.ProductCustomerChangePreview{}, ErrConflict
		}
		target.AuthOrganizationID = before.AuthOrganizationID
	}
	target.Revision = catalog.Revision
	p := api.ProductCustomerChangePreview{Before: before, After: target, BeforeCatalog: beforeCatalog, Catalog: catalog, HeadRevision: head.Revision, Period: productcontrol.Period(now)}
	p.Hash = productOwnerHash(struct {
		Account, App string
		Preview      api.ProductCustomerChangePreview
	}{account, app, p})
	return p, nil
}

func validateProductChange(req api.ProductCustomerChangeRequest) error {
	hash, err := hex.DecodeString(req.ExpectedHash)
	if !validProductUUID(req.ID) || !validProductUUID(req.Customer.TenantID) || req.Customer.Revision == "" || err != nil || len(hash) != sha256.Size || hex.EncodeToString(hash) != req.ExpectedHash || validateProductCustomerIdentity(req.Customer) != nil {
		return ErrInvalidArgument
	}
	return nil
}

func productChangeFingerprint(account, app string, req api.ProductCustomerChangeRequest) string {
	return productOwnerHash(struct {
		Account, App string
		Request      api.ProductCustomerChangeRequest
	}{account, app, req})
}

type productOwnerCursor struct {
	Scope  string    `json:"scope"`
	Period string    `json:"period,omitempty"`
	ID     string    `json:"id"`
	At     time.Time `json:"at,omitempty"`
}

func productOwnerCursorScope(kind, account, app, tenant, period string) string {
	return productOwnerHash([]string{"product-owner/v1", kind, account, app, tenant, period})
}

func decodeProductOwnerCursor(raw, scope string, runs bool) (productOwnerCursor, error) {
	c := productOwnerCursor{Scope: scope}
	if raw == "" {
		return c, nil
	}
	if len(raw) > api.ProductControlOwnerCursorMaxBytes {
		return c, ErrInvalidArgument
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return c, ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(decoded))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Scope != scope || !validProductUUID(c.ID) {
		return c, ErrInvalidArgument
	}
	if (runs && (c.At.IsZero() || c.Period != "")) || (!runs && (productcontrol.ValidatePeriod(c.Period) != nil || !c.At.IsZero())) {
		return c, ErrInvalidArgument
	}
	return c, nil
}

func encodeProductOwnerCursor(c productOwnerCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
