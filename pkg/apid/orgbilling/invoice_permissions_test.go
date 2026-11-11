package orgbilling_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/orgbilling"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInvoiceEnrichmentUsesBillingPermission(t *testing.T) {
	for _, role := range []state.OrgRole{state.OrgRoleOwner, state.OrgRoleBilling, state.OrgRoleAdmin, state.OrgRoleDeveloper, state.OrgRoleViewer} {
		t.Run(string(role), func(t *testing.T) {
			actor := state.Account{ID: "member"}
			ctx := authmw.WithPrincipal(context.Background(), actor, nil, &state.OrgMembership{OrgID: "company", AccountID: actor.ID, Role: role})
			want := role == state.OrgRoleOwner || role == state.OrgRoleBilling
			if got := orgbilling.CanReadInvoices(httptest.NewRequest("GET", "/", nil).WithContext(ctx), state.Account{ID: "company", ResourceOrgID: "company"}); got != want {
				t.Fatalf("role=%s invoice permission=%v want=%v", role, got, want)
			}
		})
	}
	if orgbilling.CanReadInvoices(httptest.NewRequest("GET", "/", nil).WithContext(context.Background()), state.Account{ID: "company", ResourceOrgID: "company"}) {
		t.Fatal("missing principal authorized")
	}
	if !orgbilling.CanReadInvoices(httptest.NewRequest("GET", "/", nil).WithContext(context.Background()), state.Account{ID: "personal"}) {
		t.Fatal("personal account lost its invoices")
	}
}
