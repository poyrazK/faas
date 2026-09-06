// Command capabilities-md generates the checked-in product capability matrix
// from pkg/productcap/catalog.json. The output is deterministic and is checked
// by CI so product claims cannot drift from the registry.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/productcap"
)

func main() {
	catalog, err := productcap.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "capabilities-md:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.WriteString(render(catalog)); err != nil {
		fmt.Fprintln(os.Stderr, "capabilities-md: write:", err)
		os.Exit(1)
	}
}

func render(catalog productcap.Catalog) string {
	var out []byte
	out = append(out, "# Gregale API-hosting capability matrix\n\n"...)
	out = append(out, "<!-- GENERATED — do not edit by hand; regenerate with `make capabilities-md`. -->\n\n"...)
	out = append(out, "This matrix is generated from [`pkg/productcap/catalog.json`](../pkg/productcap/catalog.json). Maturity is the product promise: `internal` is operator-only, `preview` is usable with explicit qualification, `beta` is supported for production-shaped use, and `ga` is generally available.\n\n"...)
	out = append(out, "| Capability | Category | Maturity | Plans | Description | Acceptance evidence |\n|---|---|---|---|---|---|\n"...)
	for _, capability := range catalog.Capabilities {
		plans := "—"
		if len(capability.Plans) > 0 {
			plans = joinPlans(capability.Plans)
		}
		docs := capability.DocsURL
		if len(docs) > 0 && docs[0] == '/' {
			docs = ".." + docs
		} else {
			docs = capability.DocsURL
		}
		out = append(out, fmt.Sprintf("| [%s](%s) | %s | `%s` | %s | %s | `%s` |\n", capability.Name, docs, capability.Category, capability.Maturity, plans, capability.Description, capability.AcceptanceTest)...)
	}
	out = append(out, "\n## Promotion rule\n\nA capability may move from `internal` to `preview`, `beta`, or `ga` only when its acceptance evidence, documentation, plan entitlement, operational owner, and rollback/recovery procedure are updated in the same change. Numeric quotas remain defined in [`pkg/api/limits.go`](../pkg/api/limits.go).\n"...)
	return string(out)
}

func joinPlans(plans []string) string {
	result := ""
	for i, plan := range plans {
		if i > 0 {
			result += ", "
		}
		result += "`" + plan + "`"
	}
	return result
}
