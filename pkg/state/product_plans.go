package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

var ErrProductPlanChangeStale = errors.New("state: customer plan review changed")
var ErrProductPlanNotOffered = errors.New("state: customer plan transition is not offered")

// Only the live Auth boundary constructs this scope, never a request body.
type ProductPlanScope struct {
	AccountID, AppID, TenantID, AuthApp, AuthAppID, OrganizationID, Role string
}

type ProductSelfServiceStore interface {
	GetProductPlanOptions(context.Context, ProductPlanScope) (api.ProductPlanOptions, error)
	PreviewProductPlanChange(context.Context, ProductPlanScope, api.ProductPlanSelection) (api.ProductPlanChangePreview, error)
	ApplyProductPlanChange(context.Context, ProductPlanScope, api.ProductPlanChangeConfirmation) (api.ProductPlanChangeReceipt, error)
	GetProductPlanChange(context.Context, ProductPlanScope, string) (api.ProductPlanChangeReceipt, error)
	ListProductPlanChanges(context.Context, ProductPlanScope, string) (api.ProductPlanChanges, error)
}

type productPlanView struct {
	head, current api.ProductCatalog
	customer      api.ProductCustomer
	starts        map[string]int64
	period        string
}

func validateProductPlanScope(s ProductPlanScope) error {
	if !validProductUUID(s.TenantID) || !validProductUUID(s.OrganizationID) || !productcontrol.ValidAccessRole(s.Role) || s.AuthApp == "" {
		return ErrInvalidArgument
	}
	if _, err := operationUUID(s.AccountID); err != nil {
		return err
	}
	if _, err := operationUUID(s.AppID); err != nil {
		return err
	}
	_, err := operationUUID(s.AuthAppID)
	return err
}

func productPlanAuthorized(s ProductPlanScope, head api.ProductCatalog, customer api.ProductCustomer, tenant PlatformTenant) error {
	if tenant.Status != PlatformTenantActive || tenant.AccountID != s.AccountID || customer.AuthOrganizationID != s.OrganizationID || head.Spec.AuthApp != s.AuthApp || !productcontrol.EffectiveAccess(head.Spec.Access, s.Role).ManagePlan {
		return ErrProductAccess
	}
	return nil
}

func productPlanFeatures(v productPlanView) []string {
	names := map[string]bool{}
	for name := range v.current.Spec.Plans[v.customer.Plan].Features {
		names[name] = true
	}
	if v.head.Spec.SelfService != nil {
		for _, target := range v.head.Spec.SelfService.Transitions[v.customer.Plan] {
			for name := range v.head.Spec.Plans[target].Features {
				names[name] = true
			}
		}
	}
	out := []string{}
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func productPlanTerms(c api.ProductCatalog, name string, starts map[string]int64) api.ProductPlanTerms {
	p := c.Spec.Plans[name]
	out := api.ProductPlanTerms{Plan: name, Label: p.Label, Revision: c.Revision, Currency: c.Spec.Currency, Features: []api.ProductPlanFeatureTerms{}}
	for feature, grant := range p.Features {
		out.Features = append(out.Features, api.ProductPlanFeatureTerms{Name: feature, Label: c.Spec.Features[feature].Label, MonthlyStarts: grant.MonthlyStarts, UnitPriceMillicents: grant.UnitPriceMillicents, Starts: starts[feature], RemainingStarts: max(0, grant.MonthlyStarts-starts[feature])})
	}
	sort.Slice(out.Features, func(i, j int) bool { return out.Features[i].Name < out.Features[j].Name })
	return out
}

func productPlanOptions(v productPlanView) api.ProductPlanOptions {
	out := api.ProductPlanOptions{Current: productPlanTerms(v.current, v.customer.Plan, v.starts), Offers: []api.ProductPlanTerms{}, Period: v.period, HeadRevision: v.head.Revision, Suspended: v.customer.Suspended}
	if v.customer.Suspended || v.head.Spec.SelfService == nil {
		return out
	}
	for _, target := range v.head.Spec.SelfService.Transitions[v.customer.Plan] {
		if target == v.customer.Plan && v.customer.Revision == v.head.Revision {
			continue
		}
		out.Offers = append(out.Offers, productPlanTerms(v.head, target, v.starts))
	}
	sort.Slice(out.Offers, func(i, j int) bool { return out.Offers[i].Plan < out.Offers[j].Plan })
	return out
}

func productPlanPreview(s ProductPlanScope, v productPlanView, selection api.ProductPlanSelection) (api.ProductPlanChangePreview, error) {
	if !validProductUUID(selection.ID) || !productcontrol.ValidProductPlanName(selection.Plan) {
		return api.ProductPlanChangePreview{}, ErrInvalidArgument
	}
	if v.customer.Suspended {
		return api.ProductPlanChangePreview{}, ErrProductAccess
	}
	if selection.Revision != "" && selection.Revision != v.head.Revision {
		return api.ProductPlanChangePreview{}, ErrProductPlanChangeStale
	}
	if !productcontrol.PlanOffered(v.head.Spec.SelfService, v.customer.Plan, selection.Plan) || selection.Plan == v.customer.Plan && v.customer.Revision == v.head.Revision {
		return api.ProductPlanChangePreview{}, ErrProductPlanNotOffered
	}
	selection.Revision = v.head.Revision
	after := v.customer
	after.Plan, after.Revision = selection.Plan, selection.Revision
	p := api.ProductPlanChangePreview{Request: selection, BeforeCustomer: v.customer, AfterCustomer: after, Before: productPlanTerms(v.current, v.customer.Plan, v.starts), After: productPlanTerms(v.head, selection.Plan, v.starts), Period: v.period, HeadRevision: v.head.Revision}
	p.Hash = productOwnerHash(struct {
		Account, App, Tenant, AuthApp, AuthAppID, Org string
		Preview                                       api.ProductPlanChangePreview
	}{s.AccountID, s.AppID, s.TenantID, s.AuthApp, s.AuthAppID, s.OrganizationID, p})
	return p, nil
}

func validateProductPlanConfirmation(c api.ProductPlanChangeConfirmation) error {
	if !validProductUUID(c.Request.ID) || !productcontrol.ValidProductPlanName(c.Request.Plan) || validateProductExpectedHead(c.Request.Revision) != nil || c.Request.Revision == "" || validateProductExpectedHead(c.ExpectedHash) != nil || c.ExpectedHash == "" {
		return ErrInvalidArgument
	}
	return nil
}

func productPlanFingerprint(s ProductPlanScope, c api.ProductPlanChangeConfirmation) string {
	// A different authorized member/role may recover the same workspace change.
	return productOwnerHash([]any{s.AccountID, s.AppID, s.TenantID, s.AuthApp, s.AuthAppID, s.OrganizationID, c})
}

func productPlanCursor(s ProductPlanScope, raw string) (productOwnerCursor, error) {
	return decodeProductOwnerCursor(raw, productOwnerCursorScope("customer-plans", s.AccountID, s.AppID, s.TenantID, ""), true)
}

func productPlanReceipt(p api.ProductPlanChangePreview, role string) api.ProductPlanChangeReceipt {
	return api.ProductPlanChangeReceipt{ID: p.Request.ID, Preview: p, ActorRole: role, EffectiveAt: time.Now().UTC().Truncate(time.Microsecond)}
}
