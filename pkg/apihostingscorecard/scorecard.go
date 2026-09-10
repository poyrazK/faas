// Package apihostingscorecard defines the evidence contract for the
// API-hosting release scorecard. The scorecard records targets and the tests,
// make targets, and runbooks that produce their evidence; it deliberately does
// not turn an unmeasured target into a passing claim.
package apihostingscorecard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/productcap"
)

//go:embed scorecard.json
var scorecardJSON []byte

// Scorecard is the versioned API-hosting release evidence contract.
type Scorecard struct {
	Version int    `json:"version"`
	Gates   []Gate `json:"gates"`
}

// Gate describes one measurable release outcome.
type Gate struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Category     string     `json:"category"`
	Description  string     `json:"description"`
	Capabilities []string   `json:"capabilities"`
	Targets      []Target   `json:"targets"`
	Evidence     []Evidence `json:"evidence"`
}

// Target is a threshold that a future measurement must satisfy. Values are
// intentionally numeric and unit-bearing so dashboards and release tooling do
// not need to parse prose.
type Target struct {
	Metric   string  `json:"metric"`
	Operator string  `json:"operator"`
	Value    float64 `json:"value"`
	Unit     string  `json:"unit"`
}

// Evidence points to the test or operational artifact that produces a gate's
// measurement. Locators are verified by Check, so stale references fail CI.
type Evidence struct {
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
}

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	testLocator     = regexp.MustCompile(`^(.+\.go)::(Test[A-Za-z0-9_]*)$`)
	makeLocator     = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	validCategories = map[string]struct{}{
		"activation": {}, "compatibility": {}, "delivery": {},
		"reliability": {}, "security": {}, "observability": {},
		"resources": {},
	}
	validEvidenceKinds = map[string]struct{}{
		"test": {}, "make": {}, "runbook": {}, "workflow": {},
	}
	validMetrics = map[string]struct{}{
		"p50": {}, "p95": {}, "ratio": {}, "count": {},
	}
	validOperators = map[string]struct{}{"lte": {}, "gte": {}}
	validUnits     = map[string]struct{}{
		"milliseconds": {}, "seconds": {}, "percent": {}, "runs": {},
	}
)

// Load returns the embedded scorecard after structural validation.
func Load() (Scorecard, error) {
	var scorecard Scorecard
	if err := json.Unmarshal(scorecardJSON, &scorecard); err != nil {
		return Scorecard{}, fmt.Errorf("decode embedded API-hosting scorecard: %w", err)
	}
	if err := Validate(scorecard); err != nil {
		return Scorecard{}, err
	}
	return scorecard, nil
}

// JSON returns a defensive copy of the canonical scorecard bytes.
func JSON() []byte { return append([]byte(nil), scorecardJSON...) }

// Validate enforces the scorecard's closed vocabularies and deterministic
// ordering. It does not touch the filesystem; use Check for evidence checks.
func Validate(scorecard Scorecard) error {
	if scorecard.Version < 1 {
		return fmt.Errorf("version must be positive; got %d", scorecard.Version)
	}
	if len(scorecard.Gates) == 0 {
		return fmt.Errorf("gates must not be empty")
	}
	seen := make(map[string]struct{}, len(scorecard.Gates))
	ids := make([]string, 0, len(scorecard.Gates))
	for i, gate := range scorecard.Gates {
		if !idPattern.MatchString(gate.ID) {
			return fmt.Errorf("gates[%d].id must be lowercase kebab-case; got %q", i, gate.ID)
		}
		if _, ok := seen[gate.ID]; ok {
			return fmt.Errorf("duplicate gate id %q", gate.ID)
		}
		seen[gate.ID] = struct{}{}
		ids = append(ids, gate.ID)
		if strings.TrimSpace(gate.Name) == "" || strings.TrimSpace(gate.Description) == "" {
			return fmt.Errorf("gate %q needs a name and description", gate.ID)
		}
		if _, ok := validCategories[gate.Category]; !ok {
			return fmt.Errorf("gate %q has invalid category %q", gate.ID, gate.Category)
		}
		if len(gate.Capabilities) == 0 || !sort.StringsAreSorted(gate.Capabilities) {
			return fmt.Errorf("gate %q capabilities must be non-empty and sorted", gate.ID)
		}
		for j := 1; j < len(gate.Capabilities); j++ {
			if gate.Capabilities[j] == gate.Capabilities[j-1] {
				return fmt.Errorf("gate %q repeats capability %q", gate.ID, gate.Capabilities[j])
			}
		}
		if len(gate.Targets) == 0 {
			return fmt.Errorf("gate %q has no targets", gate.ID)
		}
		for _, target := range gate.Targets {
			if _, ok := validMetrics[target.Metric]; !ok {
				return fmt.Errorf("gate %q has invalid metric %q", gate.ID, target.Metric)
			}
			if _, ok := validOperators[target.Operator]; !ok {
				return fmt.Errorf("gate %q has invalid operator %q", gate.ID, target.Operator)
			}
			if target.Value <= 0 {
				return fmt.Errorf("gate %q has non-positive target %v", gate.ID, target.Value)
			}
			if _, ok := validUnits[target.Unit]; !ok {
				return fmt.Errorf("gate %q has invalid unit %q", gate.ID, target.Unit)
			}
		}
		if len(gate.Evidence) == 0 {
			return fmt.Errorf("gate %q has no evidence", gate.ID)
		}
		for _, evidence := range gate.Evidence {
			if _, ok := validEvidenceKinds[evidence.Kind]; !ok {
				return fmt.Errorf("gate %q has invalid evidence kind %q", gate.ID, evidence.Kind)
			}
			if strings.TrimSpace(evidence.Locator) == "" {
				return fmt.Errorf("gate %q has empty evidence locator", gate.ID)
			}
		}
	}
	if !sort.StringsAreSorted(ids) {
		return fmt.Errorf("gates must be sorted by id")
	}
	return nil
}

// ValidateAgainstCatalog ensures every customer-visible capability has at
// least one scorecard gate and every scorecard reference names a real
// capability. Internal capabilities are intentionally exempt: they are not
// release promises.
func ValidateAgainstCatalog(scorecard Scorecard, catalog productcap.Catalog) error {
	capabilities := make(map[string]productcap.Capability, len(catalog.Capabilities))
	for _, capability := range catalog.Capabilities {
		capabilities[capability.ID] = capability
	}
	covered := make(map[string]struct{})
	acceptanceEvidence := make(map[string]struct{})
	for _, gate := range scorecard.Gates {
		for _, id := range gate.Capabilities {
			capability, ok := capabilities[id]
			if !ok {
				return fmt.Errorf("gate %q references unknown capability %q", gate.ID, id)
			}
			if capability.Maturity != productcap.MaturityInternal {
				covered[id] = struct{}{}
			}
		}
		for _, evidence := range gate.Evidence {
			if evidence.Kind == "test" {
				acceptanceEvidence[evidence.Locator] = struct{}{}
			}
		}
	}
	for _, capability := range catalog.Capabilities {
		if capability.Maturity == productcap.MaturityInternal {
			continue
		}
		if _, ok := covered[capability.ID]; !ok {
			return fmt.Errorf("public capability %q has no scorecard gate", capability.ID)
		}
		if capability.AcceptanceTest != "" {
			if _, ok := acceptanceEvidence[capability.AcceptanceTest]; !ok {
				return fmt.Errorf("public capability %q acceptance test %q is not scorecard evidence", capability.ID, capability.AcceptanceTest)
			}
		}
	}
	return nil
}

// Check validates the embedded scorecard, its catalog coverage, and every
// evidence locator relative to repositoryRoot.
func Check(repositoryRoot string) (Scorecard, error) {
	scorecard, err := Load()
	if err != nil {
		return Scorecard{}, err
	}
	catalog, err := productcap.Load()
	if err != nil {
		return Scorecard{}, fmt.Errorf("load capability catalog: %w", err)
	}
	if err := ValidateAgainstCatalog(scorecard, catalog); err != nil {
		return Scorecard{}, err
	}
	if err := validateEvidence(repositoryRoot, scorecard); err != nil {
		return Scorecard{}, err
	}
	return scorecard, nil
}

func validateEvidence(root string, scorecard Scorecard) error {
	for _, gate := range scorecard.Gates {
		for _, evidence := range gate.Evidence {
			switch evidence.Kind {
			case "test":
				match := testLocator.FindStringSubmatch(evidence.Locator)
				if match == nil {
					return fmt.Errorf("gate %q has malformed test locator %q", gate.ID, evidence.Locator)
				}
				if err := evidenceFileContains(root, match[1], "func "+match[2]+"("); err != nil {
					return fmt.Errorf("gate %q evidence %q: %w", gate.ID, evidence.Locator, err)
				}
			case "make":
				if !makeLocator.MatchString(evidence.Locator) {
					return fmt.Errorf("gate %q has malformed make locator %q", gate.ID, evidence.Locator)
				}
				if err := evidenceFileContains(root, "Makefile", "\n"+evidence.Locator+":"); err != nil {
					return fmt.Errorf("gate %q evidence %q: %w", gate.ID, evidence.Locator, err)
				}
			case "runbook", "workflow":
				path := strings.SplitN(evidence.Locator, "#", 2)[0]
				if err := evidenceFileContains(root, path, ""); err != nil {
					return fmt.Errorf("gate %q evidence %q: %w", gate.ID, evidence.Locator, err)
				}
			}
		}
	}
	return nil
}

func evidenceFileContains(root, name, needle string) error {
	path := filepath.Join(root, filepath.Clean(name))
	if !strings.HasPrefix(path, filepath.Clean(root)+string(os.PathSeparator)) {
		return fmt.Errorf("path escapes repository root")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if needle != "" && !strings.Contains(string(body), needle) {
		return fmt.Errorf("%s does not contain %q", name, needle)
	}
	return nil
}
