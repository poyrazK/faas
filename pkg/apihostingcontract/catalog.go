// Package apihostingcontract owns the production-shaped source fixtures used
// to keep API detection and framework inference stable across releases.
package apihostingcontract

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

//go:embed catalog.json
var catalogJSON []byte

// Fixture is one source-tree contract case.
type Fixture struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Files       map[string]string `json:"files"`
	Expected    Expected          `json:"expected"`
}

// Expected is the profile contract asserted by the fixture runner.
type Expected struct {
	Framework    string `json:"framework"`
	Port         int    `json:"port"`
	StartCommand string `json:"start_command"`
	Inferred     bool   `json:"inferred"`
}

// Catalog is the versioned fixture catalog.
type Catalog struct {
	Version  int       `json:"version"`
	Fixtures []Fixture `json:"fixtures"`
}

// Load returns and validates the embedded fixture catalog.
func Load() (Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode fixture catalog: %w", err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// Validate checks identity, source shape, and expected profile fields. The
// runner performs behavioral validation against frameworkprofile separately.
func Validate(catalog Catalog) error {
	if catalog.Version < 1 {
		return fmt.Errorf("version must be positive; got %d", catalog.Version)
	}
	if len(catalog.Fixtures) == 0 {
		return fmt.Errorf("fixtures must not be empty")
	}
	idPattern := regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	seen := make(map[string]struct{}, len(catalog.Fixtures))
	ids := make([]string, 0, len(catalog.Fixtures))
	for i, fixture := range catalog.Fixtures {
		if fixture.ID == "" || !idPattern.MatchString(fixture.ID) {
			return fmt.Errorf("fixtures[%d].id must be lowercase kebab-case; got %q", i, fixture.ID)
		}
		if _, ok := seen[fixture.ID]; ok {
			return fmt.Errorf("duplicate fixture id %q", fixture.ID)
		}
		seen[fixture.ID] = struct{}{}
		ids = append(ids, fixture.ID)
		if fixture.Description == "" || len(fixture.Files) == 0 {
			return fmt.Errorf("fixture %q needs a description and at least one file", fixture.ID)
		}
		if fixture.Expected.Framework == "" || fixture.Expected.Port < 1 || fixture.Expected.Port > 65535 {
			return fmt.Errorf("fixture %q has invalid expected profile", fixture.ID)
		}
	}
	if !sort.StringsAreSorted(ids) {
		return fmt.Errorf("fixtures must be sorted by id")
	}
	return nil
}
