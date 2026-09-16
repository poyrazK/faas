// Package productstandards contains Gregale's machine-readable standards
// compatibility registry.
//
// The registry is intentionally separate from productcap: a product
// capability says what a customer can use, while a standard entry says which
// external contract that capability implements and where its boundary is.
package productstandards

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

// Status describes Gregale's current compatibility claim.
type Status string

const (
	StatusSupported      Status = "supported"
	StatusPartial        Status = "partial"
	StatusPlanned        Status = "planned"
	StatusAlignmentDraft Status = "alignment-draft"
)

// ConformanceTier identifies the kind of executable evidence behind a
// compatibility claim. The tier is a routing hint for CI: unit and
// integration fixtures run in ordinary Go jobs, while e2e and metal fixtures
// require their respective acceptance environments.
type ConformanceTier string

const (
	ConformanceUnit        ConformanceTier = "unit"
	ConformanceIntegration ConformanceTier = "integration"
	ConformanceE2E         ConformanceTier = "e2e"
	ConformanceMetal       ConformanceTier = "metal"
)

// ConformanceRef points at a test function that exercises a standard's
// published subset. Targets are repository-relative paths in the form
// path/to/file_test.go::TestName.
type ConformanceRef struct {
	Fixture string          `json:"fixture"`
	Tier    ConformanceTier `json:"tier"`
	Target  string          `json:"target"`
}

// Standard is one externally defined contract tracked by Gregale.
type Standard struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	Category    string           `json:"category"`
	Surface     string           `json:"surface"`
	Status      Status           `json:"status"`
	Scope       string           `json:"scope"`
	Limitations string           `json:"limitations"`
	DocsURL     string           `json:"docs_url"`
	Evidence    string           `json:"evidence"`
	Conformance []ConformanceRef `json:"conformance,omitempty"`
}

// Catalog is the versioned on-disk standards registry.
type Catalog struct {
	Version   int        `json:"version"`
	Standards []Standard `json:"standards"`
}

// Load returns the embedded catalog after applying all registry invariants.
func Load() (Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode embedded standards catalog: %w", err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// JSON returns a copy of the canonical machine-readable registry bytes.
func JSON() []byte {
	return append([]byte(nil), catalogJSON...)
}

var idPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Validate enforces a closed vocabulary and complete evidence for every
// standard claim. It does not assert that evidence paths still exist: some
// evidence is an acceptance gate on a metal host or an external provider.
func Validate(catalog Catalog) error {
	if catalog.Version < 1 {
		return fmt.Errorf("version must be positive; got %d", catalog.Version)
	}
	if len(catalog.Standards) == 0 {
		return fmt.Errorf("standards must not be empty")
	}

	seen := make(map[string]struct{}, len(catalog.Standards))
	ids := make([]string, 0, len(catalog.Standards))
	for i, standard := range catalog.Standards {
		if standard.ID == "" || !idPattern.MatchString(standard.ID) {
			return fmt.Errorf("standards[%d].id must be lowercase kebab-case; got %q", i, standard.ID)
		}
		if _, exists := seen[standard.ID]; exists {
			return fmt.Errorf("duplicate standard id %q", standard.ID)
		}
		seen[standard.ID] = struct{}{}
		ids = append(ids, standard.ID)
		requiredFields := []struct {
			name  string
			value string
		}{
			{name: "name", value: standard.Name},
			{name: "version", value: standard.Version},
			{name: "category", value: standard.Category},
			{name: "surface", value: standard.Surface},
			{name: "scope", value: standard.Scope},
			{name: "limitations", value: standard.Limitations},
			{name: "docs_url", value: standard.DocsURL},
			{name: "evidence", value: standard.Evidence},
		}
		for _, field := range requiredFields {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("standard %q has empty %s", standard.ID, field.name)
			}
		}
		if !validStatus(standard.Status) {
			return fmt.Errorf("standard %q has invalid status %q", standard.ID, standard.Status)
		}
		if standard.Status == StatusAlignmentDraft && standard.Category != "compliance" {
			return fmt.Errorf("standard %q uses alignment-draft outside compliance", standard.ID)
		}
		if requiresConformance(standard.Status) && len(standard.Conformance) == 0 {
			return fmt.Errorf("standard %q has %q status but no conformance fixtures", standard.ID, standard.Status)
		}
		if standard.Status == StatusPlanned && len(standard.Conformance) > 0 {
			return fmt.Errorf("planned standard %q must not claim conformance fixtures", standard.ID)
		}
		if err := validateConformanceRefs(standard.ID, standard.Conformance); err != nil {
			return err
		}
		if !validDocsURL(standard.DocsURL) {
			return fmt.Errorf("standard %q has invalid docs_url %q", standard.ID, standard.DocsURL)
		}
	}
	if !sort.StringsAreSorted(ids) {
		return fmt.Errorf("standards must be sorted by id")
	}
	return nil
}

func validStatus(status Status) bool {
	switch status {
	case StatusSupported, StatusPartial, StatusPlanned, StatusAlignmentDraft:
		return true
	default:
		return false
	}
}

func requiresConformance(status Status) bool {
	return status == StatusSupported || status == StatusPartial
}

func validateConformanceRefs(standardID string, refs []ConformanceRef) error {
	seen := make(map[string]struct{}, len(refs))
	for i, ref := range refs {
		if ref.Fixture == "" || !idPattern.MatchString(ref.Fixture) {
			return fmt.Errorf("standard %q conformance[%d].fixture must be lowercase kebab-case; got %q", standardID, i, ref.Fixture)
		}
		if _, exists := seen[ref.Fixture]; exists {
			return fmt.Errorf("standard %q has duplicate conformance fixture %q", standardID, ref.Fixture)
		}
		seen[ref.Fixture] = struct{}{}
		if !validConformanceTier(ref.Tier) {
			return fmt.Errorf("standard %q conformance fixture %q has invalid tier %q", standardID, ref.Fixture, ref.Tier)
		}
		if !validConformanceTarget(ref.Target) {
			return fmt.Errorf("standard %q conformance fixture %q has invalid target %q", standardID, ref.Fixture, ref.Target)
		}
	}
	return nil
}

func validConformanceTier(tier ConformanceTier) bool {
	switch tier {
	case ConformanceUnit, ConformanceIntegration, ConformanceE2E, ConformanceMetal:
		return true
	default:
		return false
	}
}

func validConformanceTarget(target string) bool {
	targetPath, testName, ok := strings.Cut(target, "::")
	if !ok || strings.TrimSpace(targetPath) == "" || strings.TrimSpace(testName) == "" {
		return false
	}
	if strings.Contains(testName, "::") || strings.Contains(targetPath, "\\") {
		return false
	}
	clean := path.Clean(strings.TrimSpace(targetPath))
	return !strings.HasPrefix(clean, "/") && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func validDocsURL(raw string) bool {
	if strings.HasPrefix(raw, "/docs/") {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
