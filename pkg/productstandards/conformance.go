package productstandards

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckConformance verifies that every executable evidence reference in the
// catalog resolves to a real Go or Node.js test and validates the published
// AsyncAPI document against the pinned official schema. It does not run the
// tests: the existing unit, integration, e2e, and metal CI jobs own that
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
		if standard.ID == "asyncapi-3" {
			body, err := os.ReadFile(filepath.Join(root, "api", "asyncapi.yaml"))
			if err != nil {
				return fmt.Errorf("read AsyncAPI contract: %w", err)
			}
			if err := ValidateAsyncAPI(body); err != nil {
				return err
			}
		}
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
			if !hasConformanceTest(string(body), path, testName) {
				return fmt.Errorf("standard %q fixture %q: test %q not found in %s", standard.ID, ref.Fixture, testName, path)
			}
		}
	}
	return nil
}

func hasConformanceTest(body, filePath, testName string) bool {
	if strings.HasSuffix(filePath, ".test.js") || strings.HasSuffix(filePath, ".spec.js") {
		return strings.Contains(body, "test('"+testName+"'") || strings.Contains(body, `test("`+testName+`"`)
	}
	return strings.Contains(body, "func "+testName+"(")
}
