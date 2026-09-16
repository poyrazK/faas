// Command standards-md generates the checked-in standards compatibility
// matrix from pkg/productstandards/catalog.json.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/productstandards"
)

func main() {
	catalog, err := productstandards.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "standards-md:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.WriteString(render(catalog)); err != nil {
		fmt.Fprintln(os.Stderr, "standards-md: write:", err)
		os.Exit(1)
	}
}

func render(catalog productstandards.Catalog) string {
	var out strings.Builder
	out.WriteString("# Gregale standards compatibility matrix\n\n")
	out.WriteString("<!-- GENERATED — do not edit by hand; regenerate with `make standards-md`. -->\n\n")
	out.WriteString("This matrix is generated from [`pkg/productstandards/catalog.json`](../pkg/productstandards/catalog.json). `supported` means Gregale exposes the standard as a tested contract; `partial` means a documented subset or adapter; `planned` means no current customer contract; `alignment-draft` is compliance evidence in progress, not certification. Conformance names the executable fixture and the CI tier that runs it.\n\n")
	out.WriteString("| Standard | Version | Status | Surface | Scope | Limitations | Evidence | Conformance |\n|---|---|---|---|---|---|---|---|\n")
	for _, standard := range catalog.Standards {
		docs := standard.DocsURL
		if strings.HasPrefix(docs, "/") {
			docs = ".." + docs
		}
		fmt.Fprintf(&out, "| [%s](%s) | %s | `%s` | %s | %s | %s | %s | %s |\n",
			cell(standard.Name), docs, standard.Version, cell(string(standard.Status)),
			cell(standard.Surface), cell(standard.Scope), cell(standard.Limitations), cell(standard.Evidence), conformanceCell(standard.Conformance))
	}
	out.WriteString("\n## Adding a standard\n\nAdd a row only when the version, supported subset, customer surface, limitations, documentation, and evidence can be named. A runtime or protocol expansion should first add its conformance fixture, then promote the row from `planned` or `partial` in the same change.\n")
	return out.String()
}

func conformanceCell(refs []productstandards.ConformanceRef) string {
	if len(refs) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, fmt.Sprintf("`%s` · `%s` · `%s`", ref.Fixture, ref.Tier, ref.Target))
	}
	return cell(strings.Join(parts, "<br>"))
}

func cell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(strings.ReplaceAll(value, "\n", " "), "\r", " ")
}
