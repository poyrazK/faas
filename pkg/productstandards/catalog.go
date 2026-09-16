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

// Standard is one externally defined contract tracked by Gregale.
type Standard struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Category    string `json:"category"`
	Surface     string `json:"surface"`
	Status      Status `json:"status"`
	Scope       string `json:"scope"`
	Limitations string `json:"limitations"`
	DocsURL     string `json:"docs_url"`
	Evidence    string `json:"evidence"`
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

func validDocsURL(raw string) bool {
	if strings.HasPrefix(raw, "/docs/") {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
