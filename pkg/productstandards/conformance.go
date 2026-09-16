package productstandards

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckConformance verifies that every executable evidence reference in the
// catalog resolves to a real Go test function. It intentionally does not run
// the tests: the existing unit, integration, e2e, and metal CI jobs own that
// execution and expose the result in their respective acceptance gates.
func CheckConformance(catalog Catalog, repoRoot string) error {
	if err := Validate(catalog); err != nil {
		return err
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	for _, standard := range catalog.Standards {
		for _, ref := range standard.Conformance {
			path, testName, _ := strings.Cut(ref.Target, "::")
			fullPath := filepath.Join(root, filepath.FromSlash(path))
			info, err := os.Stat(fullPath)
			if err != nil {
				return fmt.Errorf("standard %q fixture %q: target %q: %w", standard.ID, ref.Fixture, ref.Target, err)
			}
			if info.IsDir() {
				return fmt.Errorf("standard %q fixture %q: target %q is a directory", standard.ID, ref.Fixture, ref.Target)
			}
			body, err := os.ReadFile(fullPath)
			if err != nil {
				return fmt.Errorf("standard %q fixture %q: read %q: %w", standard.ID, ref.Fixture, ref.Target, err)
			}
			if !strings.Contains(string(body), "func "+testName+"(") {
				return fmt.Errorf("standard %q fixture %q: test %q not found in %s", standard.ID, ref.Fixture, testName, path)
			}
		}
	}
	return nil
}
