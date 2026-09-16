// Command standards-conformance verifies that registry claims have executable
// evidence references pointing at real repository test functions.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/productstandards"
)

func main() {
	catalog, err := productstandards.Load()
	if err != nil {
		fail(err)
	}
	if err := productstandards.CheckConformance(catalog, "."); err != nil {
		fail(err)
	}

	refs := 0
	for _, standard := range catalog.Standards {
		refs += len(standard.Conformance)
	}
	fmt.Printf("standards-conformance: OK (%d standards, %d executable fixtures)\n", len(catalog.Standards), refs)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "standards-conformance:", err)
	os.Exit(1)
}
