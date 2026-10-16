// obs-rule-catalog generates the compiled operator expectation sets.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/obsrulecatalog"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "verify the generated catalog without writing")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "obs-rule-catalog: no positional arguments accepted")
		os.Exit(2)
	}
	generated, err := obsrulecatalog.Generate(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "obs-rule-catalog:", err)
		os.Exit(1)
	}
	destination := filepath.Join(*root, "pkg/obsrulecatalog/catalog.json")
	if *check {
		existing, readErr := os.ReadFile(destination)
		if readErr != nil || !bytes.Equal(existing, generated) {
			fmt.Fprintln(os.Stderr, "expected rule catalog differs; run make obs-rule-catalog-generate")
			os.Exit(1)
		}
		fmt.Println("expected rule catalog: current")
		return
	}
	if err := os.WriteFile(destination, generated, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write expected rule catalog:", err)
		os.Exit(1)
	}
}
