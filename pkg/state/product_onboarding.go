package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

var ErrProductOnboardingStale = errors.New("state: Product Control onboarding review changed")

// Auth verification belongs to the product service. These methods atomically
// recheck its verified realm against the live catalog and owned Auth app.
type ProductOnboardingStore interface {
	PreviewProductOnboarding(context.Context, string, string, api.ProductOnboardingRequest) (api.ProductOnboardingPreview, error)
	ApplyProductOnboarding(context.Context, string, string, api.ProductOnboardingConfirmation) (api.ProductOnboardingReceipt, error)
	GetProductOnboarding(context.Context, string, string, string) (api.ProductOnboardingStatus, error)
	ProductOnboardingByTenant(context.Context, string, string, string) (api.ProductOnboardingStatus, error)
	ObserveProductCustomerAccess(context.Context, string, string, string, string, string) error
}

func validateProductOnboarding(req api.ProductOnboardingRequest) error {
	if !validProductUUID(req.ID) || !validProductUUID(req.OrganizationID) || !productOnboardingText(req.OrganizationName, api.ProductControlOnboardingNameMaxBytes) || !productOnboardingText(req.AuthApp, api.ProductControlMaxLabelBytes) || !productOnboardingText(req.Plan, api.ProductControlMaxLabelBytes) {
		return ErrInvalidArgument
	}
	if id, err := uuid.Parse(req.AuthAppID); err != nil || id == uuid.Nil {
		return ErrInvalidArgument
	}
	if req.Revision != "" && validateProductExpectedHead(req.Revision) != nil {
		return ErrInvalidArgument
	}
	if req.TenantID != "" {
		if !validProductUUID(req.TenantID) || req.ExternalRef != "" || req.Name != "" {
			return ErrInvalidArgument
		}
	} else if !productOnboardingText(req.ExternalRef, api.ProductControlOnboardingExternalRefMaxBytes) || !productOnboardingText(req.Name, api.ProductControlOnboardingNameMaxBytes) {
		return ErrInvalidArgument
	}
	return nil
}

func productOnboardingText(value string, limit int) bool {
	return productcontrol.ValidOnboardingText(value, limit)
}

func sameProductID(a, b string) bool {
	x, e := uuid.Parse(a)
	y, f := uuid.Parse(b)
	return e == nil && f == nil && x != uuid.Nil && x == y
}

func productOnboardingPreview(account, app string, req api.ProductOnboardingRequest, head, catalog api.ProductCatalog, tenant *PlatformTenant, before *api.ProductCustomer, count, limit int, now time.Time) (api.ProductOnboardingPreview, error) {
	p := api.ProductOnboardingPreview{Request: req, Catalog: catalog, HeadRevision: head.Revision, Period: productcontrol.Period(now), Before: before, CreatesTenant: tenant == nil}
	if head.Spec.AuthApp != req.AuthApp {
		return p, ErrProductOnboardingStale
	}
	if _, exists := catalog.Spec.Plans[req.Plan]; !exists {
		return p, ErrNotFound
	}
	p.Request.Revision = catalog.Revision
	if tenant == nil {
		if count >= limit {
			return p, &PlatformTenantQuotaError{Limit: limit, Observed: count}
		}
		p.Tenant = api.ProductOnboardingTenant{ExternalRef: req.ExternalRef, Name: req.Name, Status: PlatformTenantActive}
	} else {
		if tenant.Status != PlatformTenantActive {
			return p, ErrProductAccess
		}
		if req.TenantID == "" && tenant.Name != req.Name {
			return p, ErrConflict
		}
		p.Tenant = onboardingTenant(*tenant)
	}
	// Existing customer terms and established identity links are preserved.
	// Adding a first link is explicitly reviewed; other changes use the owner flow.
	if before != nil && (before.Suspended || before.Plan != req.Plan || before.Revision != catalog.Revision || (before.AuthOrganizationID != "" && before.AuthOrganizationID != req.OrganizationID)) {
		return p, ErrConflict
	}
	p.Hash = productOwnerHash(struct {
		Account, App string
		Preview      api.ProductOnboardingPreview
	}{account, app, p})
	return p, nil
}

func onboardingTenant(t PlatformTenant) api.ProductOnboardingTenant {
	return api.ProductOnboardingTenant{ID: t.ID, ExternalRef: t.ExternalRef, Name: t.Name, Status: t.Status}
}

func productOnboardingFingerprint(account, app string, c api.ProductOnboardingConfirmation) string {
	return productOwnerHash(struct {
		Account, App string
		Confirmation api.ProductOnboardingConfirmation
	}{account, app, c})
}

func validateProductOnboardingConfirmation(c api.ProductOnboardingConfirmation) error {
	if validateProductOnboarding(c.Request) != nil || c.Request.Revision == "" || c.ExpectedHash == "" || validateProductExpectedHead(c.ExpectedHash) != nil {
		return ErrInvalidArgument
	}
	return nil
}

func newProductOnboardingReceipt(p api.ProductOnboardingPreview, tenant PlatformTenant, now time.Time) api.ProductOnboardingReceipt {
	return api.ProductOnboardingReceipt{ID: p.Request.ID, PreviewHash: p.Hash, Request: p.Request, Tenant: onboardingTenant(tenant), CreatedTenant: p.CreatesTenant, Customer: api.ProductCustomer{TenantID: tenant.ID, Plan: p.Request.Plan, Revision: p.Request.Revision, AuthOrganizationID: p.Request.OrganizationID}, CreatedAt: now.UTC().Truncate(time.Microsecond)}
}

func productOnboardingStatus(receipt api.ProductOnboardingReceipt, current *api.ProductCustomer, tenant PlatformTenant, head api.ProductCatalog, authApp App, access *time.Time, success *api.ProductOnboardingSuccess) api.ProductOnboardingStatus {
	status := api.ProductOnboardingStatus{Receipt: receipt, CurrentCustomer: current, FirstSuccess: success}
	status.SetupCurrent = current != nil && *current == receipt.Customer
	status.AccessConfigured = current != nil && !current.Suspended && tenant.Status == PlatformTenantActive && current.AuthOrganizationID == receipt.Request.OrganizationID && head.Spec.AuthApp == receipt.Request.AuthApp && sameProductID(authApp.ID, receipt.Request.AuthAppID) && authApp.Status != AppDeleted
	if status.AccessConfigured {
		status.AccessVerifiedAt = access
	}
	return status
}
