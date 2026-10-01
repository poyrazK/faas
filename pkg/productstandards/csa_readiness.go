package productstandards

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const csaReadinessMapPath = "docs/compliance/csa-ccm-v4.1-readiness.json"

var csaReadinessAreaIDs = []string{
	"governance-assurance",
	"identity-access",
	"platform-infrastructure",
	"data-privacy",
	"continuity-recovery",
	"facilities-suppliers",
	"development-supply-chain",
	"monitoring-incident-response",
	"workforce-endpoint",
	"customer-portability",
}

var csaReadinessParties = map[string]struct{}{
	"gregale":          {},
	"hosting_provider": {},
	"customer":         {},
}

var csaReadinessStatuses = map[string]struct{}{
	"partial":   {},
	"inherited": {},
	"open":      {},
}

// CSAReadinessMap is Gregale's original high-level evidence index. It points
// to the official CSA resource without embedding CSA's control inventory.
type CSAReadinessMap struct {
	StandardID    string             `json:"standard_id"`
	SourceVersion string             `json:"source_version"`
	SourceURL     string             `json:"source_url"`
	PublicUseNote string             `json:"public_use_note"`
	Areas         []CSAReadinessArea `json:"areas"`
}

// CSAReadinessArea assigns internal responsibility for a Gregale-authored
// evidence area and records the current gap.
type CSAReadinessArea struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	AccountableParty  string   `json:"accountable_party"`
	SupportingParties []string `json:"supporting_parties"`
	Status            string   `json:"status"`
	Evidence          []string `json:"evidence"`
	Gap               string   `json:"gap"`
}

// CheckCSAReadiness validates the high-level CSA CCM readiness map and its
// repository evidence references. It deliberately does not validate or bundle
// CSA control identifiers or descriptions.
func CheckCSAReadiness(repoRoot string) (int, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return 0, fmt.Errorf("resolve repository root: %w", err)
	}
	mapPath := filepath.Join(root, filepath.FromSlash(csaReadinessMapPath))
	body, err := os.ReadFile(mapPath)
	if err != nil {
		return 0, fmt.Errorf("read CSA CCM readiness map: %w", err)
	}
	var readiness CSAReadinessMap
	if err := json.Unmarshal(body, &readiness); err != nil {
		return 0, fmt.Errorf("decode CSA CCM readiness map: %w", err)
	}
	if err := ValidateCSAReadiness(readiness, root); err != nil {
		return 0, err
	}
	return len(readiness.Areas), nil
}

// ValidateCSAReadiness checks that every Gregale-defined readiness area has
// one accountable party, a known status, evidence paths, and an explicit gap.
func ValidateCSAReadiness(readiness CSAReadinessMap, repoRoot string) error {
	if readiness.StandardID != "csa-ccm-v4-1" {
		return fmt.Errorf("CSA readiness standard_id = %q, want csa-ccm-v4-1", readiness.StandardID)
	}
	if readiness.SourceVersion != "CCM 4.1 / CAIQ 4.1" {
		return fmt.Errorf("CSA readiness source_version = %q, want CCM 4.1 / CAIQ 4.1", readiness.SourceVersion)
	}
	if readiness.SourceURL != "https://cloudsecurityalliance.org/artifacts/cloud-controls-matrix-v4-1" {
		return fmt.Errorf("CSA readiness source_url must point at the official CCM v4.1 resource")
	}
	if strings.TrimSpace(readiness.PublicUseNote) == "" {
		return fmt.Errorf("CSA readiness public_use_note must explain the scope of the public evidence index")
	}
	if len(readiness.Areas) != len(csaReadinessAreaIDs) {
		return fmt.Errorf("CSA readiness area count = %d, want %d", len(readiness.Areas), len(csaReadinessAreaIDs))
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	wantAreas := make(map[string]struct{}, len(csaReadinessAreaIDs))
	for _, id := range csaReadinessAreaIDs {
		wantAreas[id] = struct{}{}
	}
	seenAreas := make(map[string]struct{}, len(readiness.Areas))
	for _, area := range readiness.Areas {
		if !idPattern.MatchString(area.ID) {
			return fmt.Errorf("CSA readiness area has invalid id %q", area.ID)
		}
		if _, ok := wantAreas[area.ID]; !ok {
			return fmt.Errorf("CSA readiness has unexpected area %q", area.ID)
		}
		if _, duplicate := seenAreas[area.ID]; duplicate {
			return fmt.Errorf("CSA readiness has duplicate area %q", area.ID)
		}
		seenAreas[area.ID] = struct{}{}
		if strings.TrimSpace(area.Name) == "" {
			return fmt.Errorf("CSA readiness area %q has empty name", area.ID)
		}
		if err := validateReadinessParty(area.ID, "accountable_party", area.AccountableParty); err != nil {
			return err
		}
		seenParties := map[string]struct{}{area.AccountableParty: {}}
		for _, party := range area.SupportingParties {
			if err := validateReadinessParty(area.ID, "supporting_party", party); err != nil {
				return err
			}
			if _, duplicate := seenParties[party]; duplicate {
				return fmt.Errorf("CSA readiness area %q repeats party %q", area.ID, party)
			}
			seenParties[party] = struct{}{}
		}
		if _, ok := csaReadinessStatuses[area.Status]; !ok {
			return fmt.Errorf("CSA readiness area %q has invalid status %q", area.ID, area.Status)
		}
		if len(area.Evidence) == 0 {
			return fmt.Errorf("CSA readiness area %q has no evidence references", area.ID)
		}
		for _, ref := range area.Evidence {
			if err := validateReadinessEvidence(root, area.ID, ref); err != nil {
				return err
			}
		}
		if strings.TrimSpace(area.Gap) == "" {
			return fmt.Errorf("CSA readiness area %q has no gap or next review step", area.ID)
		}
	}
	for _, id := range csaReadinessAreaIDs {
		if _, ok := seenAreas[id]; !ok {
			return fmt.Errorf("CSA readiness is missing area %q", id)
		}
	}
	return nil
}

func validateReadinessParty(areaID, field, party string) error {
	if _, ok := csaReadinessParties[party]; !ok {
		return fmt.Errorf("CSA readiness area %q has invalid %s %q", areaID, field, party)
	}
	return nil
}

func validateReadinessEvidence(root, areaID, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("CSA readiness area %q has an empty evidence reference", areaID)
	}
	clean := filepath.Clean(filepath.FromSlash(ref))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("CSA readiness area %q has unsafe evidence path %q", areaID, ref)
	}
	fullPath := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, fullPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("CSA readiness area %q has evidence path outside repository %q", areaID, ref)
	}
	if _, err := os.Stat(fullPath); err != nil {
		return fmt.Errorf("CSA readiness area %q evidence %q: %w", areaID, ref, err)
	}
	return nil
}
