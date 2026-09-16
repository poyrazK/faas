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
	out.WriteString("This matrix is generated from [`pkg/productstandards/catalog.json`](../pkg/productstandards/catalog.json). `supported` means Gregale exposes the standard as a tested contract; `partial` means a documented subset or adapter; `planned` means no current customer contract; `alignment-draft` is compliance evidence in progress, not certification.\n\n")
	out.WriteString("| Standard | Version | Status | Surface | Scope | Limitations | Evidence |\n|---|---|---|---|---|---|---|\n")
	for _, standard := range catalog.Standards {
		docs := standard.DocsURL
		if strings.HasPrefix(docs, "/") {
			docs = ".." + docs
		}
		fmt.Fprintf(&out, "| [%s](%s) | %s | `%s` | %s | %s | %s | %s |\n",
			cell(standard.Name), docs, standard.Version, cell(string(standard.Status)),
			cell(standard.Surface), cell(standard.Scope), cell(standard.Limitations), cell(standard.Evidence))
	}
	out.WriteString("\n## Adding a standard\n\nAdd a row only when the version, supported subset, customer surface, limitations, documentation, and evidence can be named. A runtime or protocol expansion should first add its conformance fixture, then promote the row from `planned` or `partial` in the same change.\n")
	return out.String()
}

func cell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(strings.ReplaceAll(value, "\n", " "), "\r", " ")
}
