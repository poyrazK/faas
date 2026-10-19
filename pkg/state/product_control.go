package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

// Product request identity survives a feature's definition/deployment changes.
// Domain separation prevents collision with legacy Operations request keys.
func productOperationKey(account, app, tenant, feature, key string) string {
	digest := operations.IdentityScope(account, app, "product-control", tenant, feature, key)
	return fmt.Sprintf("%x", sha256.Sum256([]byte("product-control/v1:"+digest)))
}

var ErrProductAccess = errors.New("state: Product Control entitlement denied")
var ErrProductQuoteChanged = errors.New("state: Product Control confirmed terms changed")
var ErrProductBindingUnavailable = errors.New("state: Product Control Operation binding unavailable")

type ProductFeatureRequest struct {
	AccountID, AppID, TenantID, Feature, IdempotencyKey string
	Input                                               json.RawMessage
}

// ProductReceiptSnapshot reads the independent receipt and retained operation
// at one store boundary. Execution history is separately paginated.
type ProductReceiptSnapshot struct {
	api.ProductReceiptEvidence
	Operation *Operation
}

type ProductControlStore interface {
	PutProductCatalog(context.Context, string, string, api.ProductCatalogSpec) (api.ProductCatalog, error)
	GetProductCatalog(context.Context, string, string, string) (api.ProductCatalog, error)
	SetProductCustomer(context.Context, string, string, api.ProductCustomer) (api.ProductCustomer, error)
	GetProductCustomer(context.Context, string, string, string) (api.ProductCustomer, error)
	ProductCustomerByAuthOrganization(context.Context, string, string, string) (api.ProductCustomer, error)
	ListProductCustomers(context.Context, string, string, string) (api.ProductCustomersResponse, error)
	GetProductUsage(context.Context, string, string, string, string) (api.ProductUsage, error)
	ResolveProductFeature(context.Context, ProductFeatureRequest) (OperationDefinition, error)
	GetProductReceipt(context.Context, string, string, string) (ProductReceiptSnapshot, error)
	LookupProductSubmission(context.Context, ProductFeatureRequest) (api.OperationSubmissionLookupResponse, error)
}

func productFeatureGrant(catalog api.ProductCatalog, customer api.ProductCustomer, feature string) (api.ProductFeature, api.ProductFeatureGrant, error) {
	plan, exists := catalog.Spec.Plans[customer.Plan]
	grant, allowed := plan.Features[feature]
	f, bound := catalog.Spec.Features[feature]
	if customer.Suspended || !exists || !allowed || !bound {
		return api.ProductFeature{}, api.ProductFeatureGrant{}, ErrProductAccess
	}
	return f, grant, nil
}

func productGrant(catalog api.ProductCatalog, customer api.ProductCustomer, feature string, def OperationDefinition, enabled bool) (api.ProductFeatureGrant, error) {
	f, grant, err := productFeatureGrant(catalog, customer, feature)
	if err != nil || !enabled {
		return api.ProductFeatureGrant{}, ErrProductAccess
	}
	if f.Operation != nil {
		if f.Operation.Name != def.Spec.Name || f.Operation.Scope != def.Scope {
			return api.ProductFeatureGrant{}, ErrProductAccess
		}
	} else if f.DefinitionID != def.ID {
		return api.ProductFeatureGrant{}, ErrProductAccess
	}
	return grant, nil
}

func productReceipt(catalog api.ProductCatalog, customer api.ProductCustomer, feature string, grant api.ProductFeatureGrant, starts int64, op Operation, now time.Time) *api.ProductOperationReceipt {
	return &api.ProductOperationReceipt{Revision: catalog.Revision, Feature: feature, Plan: customer.Plan, Currency: catalog.Spec.Currency,
		Period: productcontrol.Period(now), UnitPriceMillicents: grant.UnitPriceMillicents, AdmittedAt: now,
		Admission: &api.ProductAdmissionEvidence{DefinitionID: op.DefinitionID, DefinitionRevision: op.DefinitionRevision, DeploymentID: op.DeploymentID,
			ReleaseID: op.ReleaseID, Scope: op.Scope, Name: op.Name, MonthlyStarts: grant.MonthlyStarts, StartsBefore: starts}}
}

func productCatalogError(err error) error { return fmt.Errorf("%w: %w", ErrInvalidArgument, err) }

func productUsageLines(lines map[string]api.ProductUsageLine) []api.ProductUsageLine {
	keys := make([]string, 0, len(lines))
	for key := range lines {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]api.ProductUsageLine, 0, len(keys))
	for _, key := range keys {
		out = append(out, lines[key])
	}
	return out
}

// Both stores call this inside the Operations admission transaction, after
// checking the independent Product Control idempotency namespace.
func prepareProductReceipt(op *Operation, admission OperationAdmission, catalog api.ProductCatalog, customer api.ProductCustomer, grant api.ProductFeatureGrant, starts int64, now time.Time) error {
	quote := productcontrol.Quote(op.AccountID, op.AppID, op.PlatformTenantID, admission.ProductFeature, catalog, customer, productcontrol.Period(now))
	if (admission.RequireProductQuote || admission.ExpectedProductQuote != "") && admission.ExpectedProductQuote != quote {
		return ErrProductQuoteChanged
	}
	if starts >= grant.MonthlyStarts {
		return NewOperationLimitError("product_monthly_starts", grant.MonthlyStarts, starts+1)
	}
	op.ProductControl = productReceipt(catalog, customer, admission.ProductFeature, grant, starts, *op, now)
	if admission.ExpectedProductQuote != "" {
		op.ProductControl.Admission.ConfirmedQuote = quote
	}
	return nil
}

func validateProductCustomerIdentity(customer api.ProductCustomer) error {
	if customer.AuthOrganizationID == "" {
		return nil
	}
	id, err := uuid.Parse(customer.AuthOrganizationID)
	if err != nil || id == uuid.Nil || id.String() != customer.AuthOrganizationID {
		return ErrInvalidArgument
	}
	return nil
}
