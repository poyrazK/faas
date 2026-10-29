package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// creatorBills reports whether app's customers may be billed through the
// account that created it (ADR-20261026151103 amendment 2). Billing
// connections belong to an account, and a company app keeps its creator's,
// so sign-up and the customer portal use the creator's Stripe account: only
// while the creator may still see the company's apps, the rule billing runs
// apply (account_may_see_org_resources). A failed read refuses.
func (s *server) creatorBills(ctx context.Context, app state.App) bool {
	ok, err := appaccess.Standing(ctx, s.store, app.AccountID, app, authz.OrgActionReadApp)
	if err != nil {
		s.log.WarnContext(ctx, "consumer billing: creator standing", "app_id", app.ID, "err", err)
		return false
	}
	return ok
}
