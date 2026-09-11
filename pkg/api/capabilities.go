package api

import "github.com/onebox-faas/faas/pkg/productcap"

// CapabilityMaturity and its constants are aliases of the canonical
// productcap vocabulary so API consumers can use the same lifecycle states as
// the checked-in customer matrix.
type CapabilityMaturity = productcap.Maturity

const (
	CapabilityMaturityInternal = productcap.MaturityInternal
	CapabilityMaturityPreview  = productcap.MaturityPreview
	CapabilityMaturityBeta     = productcap.MaturityBeta
	CapabilityMaturityGA       = productcap.MaturityGA
)

// CapabilitiesForPlan projects the canonical product catalog into the
// account-scoped API response. Internal capabilities are intentionally omitted
// from the customer surface. Enabled means the plan appears in the registry's
// entitlement list; numeric limits remain owned by pkg/api/limits.go.
func CapabilitiesForPlan(plan Plan) (CapabilitiesResponse, error) {
	catalog, err := productcap.Load()
	if err != nil {
		return CapabilitiesResponse{}, err
	}
	result := CapabilitiesResponse{
		RegistryVersion: catalog.Version,
		Plan:            string(plan),
		Capabilities:    make([]CapabilityStatus, 0, len(catalog.Capabilities)),
	}
	for _, capability := range catalog.Capabilities {
		if capability.Maturity == productcap.MaturityInternal {
			continue
		}
		result.Capabilities = append(result.Capabilities, CapabilityStatus{
			Key:         capability.ID,
			Name:        capability.Name,
			Category:    capability.Category,
			Description: capability.Description,
			Maturity:    capability.Maturity,
			Plans:       append([]string(nil), capability.Plans...),
			DocsURL:     capability.DocsURL,
			Acceptance:  capability.AcceptanceTest,
			Enabled:     planEntitled(capability.Plans, plan),
		})
	}
	return result, nil
}

func planEntitled(plans []string, plan Plan) bool {
	for _, entitled := range plans {
		if entitled == string(plan) {
			return true
		}
	}
	return false
}
