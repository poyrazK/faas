package productcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Quote binds consent to the authenticated customer, feature, immutable terms
// and UTC allowance month. It grants no authority and is checked under the
// admission lock after duplicate lookup. Deployment changes do not change it.
// When the grant includes starts or the customer has a trial, it also binds
// the price of the next start, so a concurrent start that uses the last
// included one, or the trial ending, makes the confirmation stale
// (ADR-20261021154511, ADR-20261021195901). paidStarts are the month's
// starts outside the trial.
func Quote(account, app, tenant, feature string, catalog api.ProductCatalog, customer api.ProductCustomer, period string, paidStarts int64, now time.Time) string {
	grant := catalog.Spec.Plans[customer.Plan].Features[feature]
	var next *int64
	if price, _, bind := StartPrice(grant, customer, paidStarts, now); bind {
		next = &price
	}
	encoded, _ := json.Marshal(struct {
		Account, App, Tenant, Feature, Revision, Plan, Currency, Period string
		Grant                                                           api.ProductFeatureGrant
		NextPrice                                                       *int64 `json:",omitempty"`
	}{account, app, tenant, feature, catalog.Revision, customer.Plan, catalog.Spec.Currency, period, grant, next})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// StartPrice is the price of a customer's next start of a feature: 0 during
// their trial or within the grant's included starts, else the unit price.
// paidStarts are the month's starts outside the trial, which included starts
// count. bind reports whether quotes bind the price.
func StartPrice(grant api.ProductFeatureGrant, customer api.ProductCustomer, paidStarts int64, now time.Time) (price int64, trial, bind bool) {
	bind = grant.IncludedStarts > 0 || customer.TrialEndsAt != nil
	if customer.InTrial(now) {
		return 0, true, bind
	}
	return grant.StartPriceMillicents(paidStarts), false, bind
}
