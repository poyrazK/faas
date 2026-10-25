package platformflags

import (
	"slices"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/productcap"
)

// generatedADR is the decision that put every capability behind a flag.
const generatedADR = "ADR-20261023115741"

// generatedSpecs derives one flag per product capability that no hand-written
// flag claims (ADR-20261023115741). The catalog is embedded and validated by
// its own tests; if it ever fails to load there are no generated flags, and the
// apid capability gate falls back to each capability's tier default.
var generatedSpecs = sync.OnceValue(func() []Spec {
	products, err := productcap.Load()
	if err != nil {
		return nil
	}
	claimed := make(map[string]bool, len(catalog))
	for _, spec := range catalog {
		if spec.Capability != "" {
			claimed[spec.Capability] = true
		}
	}
	var out []Spec
	for _, capability := range products.Capabilities {
		if claimed[capability.ID] || capability.AlwaysOn {
			continue
		}
		consumers := append([]string{"apid"}, capability.Consumers...)
		out = append(out, Spec{
			Key:         CapabilityFlagKey(capability.ID),
			Label:       capability.Name,
			Description: "Release the " + capability.Name + " capability to accounts. Off hides its API routes and dashboard pages as if they did not exist, and stops its background work.",
			Capability:  capability.ID,
			ADR:         generatedADR,
			Consumers:   consumers,
			Attributes:  ConsumerAttributes(consumers),
			Default:     TierDefault(capability.Maturity),
			Core:        capability.Core,
			Generated:   true,
			Parent:      capability.Parent,
		})
	}
	return out
})

// capabilityParents maps every product capability to its parent, so a
// hand-written flag that releases a sub-feature reports its parent too.
var capabilityParents = sync.OnceValue(func() map[string]string {
	products, err := productcap.Load()
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(products.Capabilities))
	for _, capability := range products.Capabilities {
		out[capability.ID] = capability.Parent
	}
	return out
})

// consumerAttributes are the subject attributes each daemon knows when it
// decides a capability flag. A rule may only constrain what every consumer
// of its flag can supply (ValidateTargeting).
var consumerAttributes = map[string][]Attribute{
	"apid":              {AttributePlan, AttributeOrg},
	"schedd":            {AttributePlan, AttributeOrg},
	"githubd":           nil,
	"gatewayd-internal": nil,
}

// ConsumerAttributes is the set of attributes every consumer supplies.
func ConsumerAttributes(consumers []string) []Attribute {
	var out []Attribute
	for _, attr := range []Attribute{AttributePlan, AttributeOrg} {
		all := true
		for _, consumer := range consumers {
			all = all && slices.Contains(consumerAttributes[consumer], attr)
		}
		if all {
			out = append(out, attr)
		}
	}
	return out
}

// CapabilityFlagKey is the generated flag key for a capability id.
func CapabilityFlagKey(capability string) string {
	return strings.ReplaceAll(capability, "-", "_")
}

// TierDefault is a capability flag's definition before any operator write:
// GA and beta are on for every account; preview and internal keep the kill
// switch on (so an operator can allow accounts) but are off by default.
func TierDefault(maturity productcap.Maturity) Definition {
	switch maturity {
	case productcap.MaturityGA, productcap.MaturityBeta:
		return Definition{Enabled: true, Default: true}
	default:
		return Definition{Enabled: true, Default: false}
	}
}
