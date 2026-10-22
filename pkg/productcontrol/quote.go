package productcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

// Quote binds consent to the authenticated customer, feature, immutable terms
// and UTC allowance month. It grants no authority and is checked under the
// admission lock after duplicate lookup. Deployment changes do not change it.
// When the grant includes starts, it also binds the price of the next start,
// which startsBefore decides, so a concurrent start that uses the last
// included one makes the confirmation stale (ADR-20261021154511).
func Quote(account, app, tenant, feature string, catalog api.ProductCatalog, customer api.ProductCustomer, period string, startsBefore int64) string {
	grant := catalog.Spec.Plans[customer.Plan].Features[feature]
	var next *int64
	if grant.IncludedStarts > 0 {
		price := grant.StartPriceMillicents(startsBefore)
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
