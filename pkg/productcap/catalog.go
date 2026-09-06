// Package productcap contains the customer-facing product capability catalog.
//
// This catalog is deliberately separate from provider and kernel capability
// bitsets. It answers a product question: what can a customer use, at which
// maturity, on which plan, and where is the acceptance evidence?
package productcap

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

// Maturity is the only supported product-availability vocabulary.
type Maturity string

const (
	MaturityInternal Maturity = "internal"
	MaturityPreview  Maturity = "preview"
	MaturityBeta     Maturity = "beta"
	MaturityGA       Maturity = "ga"
)

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	operatorPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// Capability is one customer-facing product capability.
type Capability struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Description    string   `json:"description"`
	Maturity       Maturity `json:"maturity"`
	Plans          []string `json:"plans"`
	DocsURL        string   `json:"docs_url"`
	OperatorFlag   string   `json:"operator_flag,omitempty"`
	AcceptanceTest string   `json:"acceptance_test"`
}

// Catalog is the versioned on-disk product capability registry.
type Catalog struct {
	Version      int          `json:"version"`
	Capabilities []Capability `json:"capabilities"`
}

// Load returns the embedded catalog after applying all registry invariants.
func Load() (Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode embedded catalog: %w", err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// JSON returns the canonical machine-readable registry bytes.
func JSON() []byte {
	return append([]byte(nil), catalogJSON...)
}

// Validate enforces the closed vocabulary and the evidence required for every
// capability. It intentionally does not validate whether a referenced test
// exists: test names can live in another package or in a metal-only suite.
func Validate(catalog Catalog) error {
	if catalog.Version < 1 {
		return fmt.Errorf("version must be positive; got %d", catalog.Version)
	}
	if len(catalog.Capabilities) == 0 {
		return fmt.Errorf("capabilities must not be empty")
	}

	seen := make(map[string]struct{}, len(catalog.Capabilities))
	for i, capability := range catalog.Capabilities {
		if capability.ID == "" || !idPattern.MatchString(capability.ID) {
			return fmt.Errorf("capabilities[%d].id must be lowercase kebab-case; got %q", i, capability.ID)
		}
		if _, exists := seen[capability.ID]; exists {
			return fmt.Errorf("duplicate capability id %q", capability.ID)
		}
		seen[capability.ID] = struct{}{}
		if strings.TrimSpace(capability.Name) == "" {
			return fmt.Errorf("capability %q has empty name", capability.ID)
		}
		if strings.TrimSpace(capability.Category) == "" {
			return fmt.Errorf("capability %q has empty category", capability.ID)
		}
		if strings.TrimSpace(capability.Description) == "" {
			return fmt.Errorf("capability %q has empty description", capability.ID)
		}
		if !validMaturity(capability.Maturity) {
			return fmt.Errorf("capability %q has invalid maturity %q", capability.ID, capability.Maturity)
		}
		if err := validatePlans(capability.ID, capability.Maturity, capability.Plans); err != nil {
			return err
		}
		if capability.OperatorFlag != "" && !operatorPattern.MatchString(capability.OperatorFlag) {
			return fmt.Errorf("capability %q has invalid operator_flag %q", capability.ID, capability.OperatorFlag)
		}
		if strings.TrimSpace(capability.AcceptanceTest) == "" || !strings.Contains(capability.AcceptanceTest, "::Test") {
			return fmt.Errorf("capability %q must name an acceptance test as package::TestName", capability.ID)
		}
		if capability.Maturity != MaturityInternal {
			if !validDocsURL(capability.DocsURL) {
				return fmt.Errorf("capability %q needs a /docs URL when maturity is %q", capability.ID, capability.Maturity)
			}
		} else if strings.TrimSpace(capability.DocsURL) == "" {
			return fmt.Errorf("internal capability %q needs an operator documentation URL", capability.ID)
		}
	}

	ids := make([]string, 0, len(catalog.Capabilities))
	for _, capability := range catalog.Capabilities {
		ids = append(ids, capability.ID)
	}
	if !sort.StringsAreSorted(ids) {
		return fmt.Errorf("capabilities must be sorted by id")
	}
	return nil
}

func validMaturity(maturity Maturity) bool {
	switch maturity {
	case MaturityInternal, MaturityPreview, MaturityBeta, MaturityGA:
		return true
	default:
		return false
	}
}

func validatePlans(id string, maturity Maturity, plans []string) error {
	known := map[string]int{"free": 0, "hobby": 1, "pro": 2, "scale": 3}
	previous := -1
	seen := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		rank, ok := known[plan]
		if !ok {
			return fmt.Errorf("capability %q has unknown plan %q", id, plan)
		}
		if _, exists := seen[plan]; exists {
			return fmt.Errorf("capability %q repeats plan %q", id, plan)
		}
		seen[plan] = struct{}{}
		if rank <= previous {
			return fmt.Errorf("capability %q plans must be ordered free, hobby, pro, scale", id)
		}
		previous = rank
	}
	if len(plans) == 0 && maturity != MaturityInternal {
		return fmt.Errorf("capability %q must have at least one entitled plan", id)
	}
	return nil
}

func validDocsURL(url string) bool {
	return strings.HasPrefix(url, "/docs/") || strings.HasPrefix(url, "https://gregale.dev/docs/")
}
