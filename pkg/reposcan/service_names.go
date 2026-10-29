package reposcan

import (
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// serviceNameWarnings reports services the others cannot call by name
// (ADR-20261026170512): a name whose DNS form (`billing_api` is called
// `billing-api`) is not a DNS label, and two services that share one DNS
// form, which leaves the name ambiguous.
func serviceNameWarnings(workloads []Workload) []string {
	var warnings []string
	byDNS := map[string]string{}
	for _, w := range workloads {
		if w.Name == "" {
			continue
		}
		dns := api.ServiceDNSName(w.Name)
		if !api.ValidServiceName(dns) {
			warnings = append(warnings, fmt.Sprintf("reposcan: service %q cannot be called by name: a service name needs letters, digits, hyphens, underscores or dots, at most 63, starting and ending with a letter or digit", w.Name))
			continue
		}
		if previous, ok := byDNS[dns]; ok && previous != w.Name {
			warnings = append(warnings, fmt.Sprintf("reposcan: services %q and %q are both called %s.internal; rename one", previous, w.Name, dns))
			continue
		}
		byDNS[dns] = w.Name
	}
	sort.Strings(warnings)
	return warnings
}
