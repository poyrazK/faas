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
func Quote(account, app, tenant, feature string, catalog api.ProductCatalog, customer api.ProductCustomer, period string) string {
	grant := catalog.Spec.Plans[customer.Plan].Features[feature]
	encoded, _ := json.Marshal(struct {
		Account, App, Tenant, Feature, Revision, Plan, Currency, Period string
		Grant                                                           api.ProductFeatureGrant
	}{account, app, tenant, feature, catalog.Revision, customer.Plan, catalog.Spec.Currency, period, grant})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
