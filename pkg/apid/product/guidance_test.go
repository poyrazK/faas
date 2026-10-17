// ADR-1604: guidance fails changed source reads and preserves role privacy.
package product

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

type guidanceProducts struct {
	state.ProductControlStore
	customer func(context.Context, string, string, string) (api.ProductCustomer, error)
	usage    func(context.Context, string, string, string, string) (api.ProductUsage, error)
}

func (p guidanceProducts) GetProductCustomer(ctx context.Context, a, app, tenant string) (api.ProductCustomer, error) {
	if p.customer != nil {
		return p.customer(ctx, a, app, tenant)
	}
	return p.ProductControlStore.GetProductCustomer(ctx, a, app, tenant)
}
func (p guidanceProducts) GetProductUsage(ctx context.Context, a, app, tenant, period string) (api.ProductUsage, error) {
	if p.usage != nil {
		return p.usage(ctx, a, app, tenant, period)
	}
	return p.ProductControlStore.GetProductUsage(ctx, a, app, tenant, period)
}

func TestProductControlGuidanceService(t *testing.T) {
	_, store, acct, app, customer := overviewFixture(t)
	ctx := t.Context()
	head, err := store.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	spec := head.Spec
	spec.Access = &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"runner": {api.ProductPermissionRunWork}, "viewer": {api.ProductPermissionViewWork}, "billing": {api.ProductPermissionViewBilling}}}
	head, err = store.PutProductCatalog(ctx, acct.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.AppBySlug(ctx, head.Spec.AuthApp)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"runner", "viewer", "billing"} {
		t.Run(role, func(t *testing.T) {
			access := api.ProductCustomerAccess{Role: role, PolicyRevision: head.Revision, Permissions: productcontrol.EffectiveAccess(spec.Access, role)}
			principal := authmw.ProductCustomerIdentity{TenantID: customer.TenantID, OrganizationID: customer.AuthOrganizationID, AuthApp: auth.Slug, AuthAppID: auth.ID, ProductSlug: app.Slug, Access: access}
			cctx := authmw.WithProductCustomer(t.Context(), acct, principal)
			// The read-only endpoint must work with just the Product Control read
			// interface; it does not need an Operation history store.
			products := guidanceProducts{ProductControlStore: store}
			view, err := customerViewWithHistory(cctx, products, acct.ID, app.ID, customer.TenantID, productcontrol.Period(time.Now()), &access, false)
			if err != nil || view.Guidance == nil {
				t.Fatalf("guidance: %+v %v", view, err)
			}
			g := view.Guidance
			if g.TenantID != customer.TenantID || g.Revision != customer.Revision || g.PolicyRevision != head.Revision || len(view.Operations) != 0 {
				t.Fatalf("source identity/history: %+v", view)
			}
			if role == "viewer" && len(g.Features) != 0 {
				t.Fatal("viewer received allowance counts")
			}
			if role == "runner" && (view.Usage != nil || len(g.Features) != 1 || g.Features[0].State != "available") {
				t.Fatalf("runner scope: %+v", view)
			}
			if role == "billing" && g.Features[0].State != "permission_required" {
				t.Fatalf("billing granted execution: %+v", g)
			}
			raw, _ := json.Marshal(g)
			for _, field := range []string{"unit_price_millicents", "amount_millicents", "definition_id", "input_schema", "succeeded", "auth_organization_id"} {
				if strings.Contains(string(raw), field) {
					t.Fatalf("guidance leaked %s", field)
				}
			}
			if role != "runner" {
				return
			}
			calls := 0
			products.customer = func(ctx context.Context, a, b, c string) (api.ProductCustomer, error) {
				calls++
				current, e := store.GetProductCustomer(ctx, a, b, c)
				if calls > 1 {
					current.Suspended = true
				}
				return current, e
			}
			if _, err := customerViewWithHistory(cctx, products, acct.ID, app.ID, customer.TenantID, productcontrol.Period(time.Now()), &access, false); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("mixed assignment returned: %v", err)
			}
			products.customer = nil
			products.usage = func(context.Context, string, string, string, string) (api.ProductUsage, error) {
				return api.ProductUsage{}, context.DeadlineExceeded
			}
			if _, err := customerViewWithHistory(cctx, products, acct.ID, app.ID, customer.TenantID, productcontrol.Period(time.Now()), &access, false); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("failed read returned guidance: %v", err)
			}
			principal.Access.PolicyRevision = strings.Repeat("0", 64)
			stale := authmw.WithProductCustomer(t.Context(), acct, principal)
			if _, err := customerViewWithHistory(stale, store, acct.ID, app.ID, customer.TenantID, productcontrol.Period(time.Now()), &access, false); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("stale permission snapshot returned: %v", err)
			}
		})
	}
	view, err := customerViewWithHistory(ctx, store, acct.ID, app.ID, customer.TenantID, "2000-01", nil, false)
	if err != nil || view.Guidance != nil {
		t.Fatalf("historical usage presented as current guidance: %+v %v", view.Guidance, err)
	}
}
