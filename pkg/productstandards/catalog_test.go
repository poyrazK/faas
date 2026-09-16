package productstandards

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCatalog(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != 1 {
		t.Fatalf("version = %d, want 1", catalog.Version)
	}
	if len(catalog.Standards) < 10 {
		t.Fatalf("standard count = %d, want at least 10", len(catalog.Standards))
	}
}

func TestValidateRejectsUnsortedIDs(t *testing.T) {
	catalog := Catalog{
		Version: 1,
		Standards: []Standard{
			{ID: "z-last", Name: "Z", Version: "1", Category: "protocol", Surface: "edge", Status: StatusSupported, Scope: "z", Limitations: "z", DocsURL: "https://example.com/z", Evidence: "z", Conformance: []ConformanceRef{{Fixture: "z-fixture", Tier: ConformanceUnit, Target: "pkg/z_test.go::TestZ"}}},
			{ID: "a-first", Name: "A", Version: "1", Category: "protocol", Surface: "edge", Status: StatusSupported, Scope: "a", Limitations: "a", DocsURL: "https://example.com/a", Evidence: "a", Conformance: []ConformanceRef{{Fixture: "a-fixture", Tier: ConformanceUnit, Target: "pkg/a_test.go::TestA"}}},
		},
	}
	if err := Validate(catalog); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("Validate error = %v, want sorted error", err)
	}
}

func TestValidateRejectsAlignmentDraftOutsideCompliance(t *testing.T) {
	catalog := Catalog{
		Version: 1,
		Standards: []Standard{{
			ID: "draft-protocol", Name: "Draft", Version: "1", Category: "protocol", Surface: "edge",
			Status: StatusAlignmentDraft, Scope: "draft", Limitations: "draft", DocsURL: "https://example.com/draft", Evidence: "draft",
		}},
	}
	if err := Validate(catalog); err == nil || !strings.Contains(err.Error(), "outside compliance") {
		t.Fatalf("Validate error = %v, want compliance error", err)
	}
}

func TestJSONReturnsCopy(t *testing.T) {
	first := JSON()
	first[0] = 'X'
	second := JSON()
	if second[0] == 'X' {
		t.Fatal("JSON returned the embedded backing slice")
	}
}

func TestValidateRequiresConformanceForSupportedAndPartial(t *testing.T) {
	for _, status := range []Status{StatusSupported, StatusPartial} {
		t.Run(string(status), func(t *testing.T) {
			catalog := Catalog{
				Version: 1,
				Standards: []Standard{{
					ID: "fixture-standard", Name: "Fixture", Version: "1", Category: "protocol", Surface: "edge",
					Status: status, Scope: "scope", Limitations: "limits", DocsURL: "https://example.com/fixture", Evidence: "evidence",
				}},
			}
			if err := Validate(catalog); err == nil || !strings.Contains(err.Error(), "no conformance fixtures") {
				t.Fatalf("Validate error = %v, want missing conformance error", err)
			}
		})
	}
}

func TestValidateRejectsPlannedConformance(t *testing.T) {
	catalog := Catalog{
		Version: 1,
		Standards: []Standard{{
			ID: "planned-standard", Name: "Planned", Version: "1", Category: "protocol", Surface: "edge",
			Status: StatusPlanned, Scope: "scope", Limitations: "limits", DocsURL: "https://example.com/planned", Evidence: "planned",
			Conformance: []ConformanceRef{{Fixture: "future-fixture", Tier: ConformanceUnit, Target: "pkg/example_test.go::TestFuture"}},
		}},
	}
	if err := Validate(catalog); err == nil || !strings.Contains(err.Error(), "must not claim conformance") {
		t.Fatalf("Validate error = %v, want planned conformance error", err)
	}
}

func TestValidateRejectsMalformedConformanceRef(t *testing.T) {
	base := Standard{
		ID: "fixture-standard", Name: "Fixture", Version: "1", Category: "protocol", Surface: "edge",
		Status: StatusSupported, Scope: "scope", Limitations: "limits", DocsURL: "https://example.com/fixture", Evidence: "evidence",
	}
	for name, ref := range map[string]ConformanceRef{
		"bad fixture id": {Fixture: "Fixture", Tier: ConformanceUnit, Target: "pkg/fixture_test.go::TestFixture"},
		"bad tier":       {Fixture: "fixture-test", Tier: "unknown", Target: "pkg/fixture_test.go::TestFixture"},
		"bad target":     {Fixture: "fixture-test", Tier: ConformanceUnit, Target: "../fixture_test.go::TestFixture"},
	} {
		t.Run(name, func(t *testing.T) {
			standard := base
			standard.Conformance = []ConformanceRef{ref}
			if err := Validate(Catalog{Version: 1, Standards: []Standard{standard}}); err == nil {
				t.Fatal("Validate accepted malformed conformance reference")
			}
		})
	}
}

func TestCheckConformanceResolvesTestTarget(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pkg", "fixture_test.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package fixture\nfunc TestFixture(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{
		Version: 1,
		Standards: []Standard{{
			ID: "fixture-standard", Name: "Fixture", Version: "1", Category: "protocol", Surface: "edge",
			Status: StatusSupported, Scope: "scope", Limitations: "limits", DocsURL: "https://example.com/fixture", Evidence: "evidence",
			Conformance: []ConformanceRef{{Fixture: "fixture-test", Tier: ConformanceUnit, Target: "pkg/fixture_test.go::TestFixture"}},
		}},
	}
	if err := CheckConformance(catalog, root); err != nil {
		t.Fatalf("CheckConformance: %v", err)
	}
}

func TestCheckConformanceRejectsMissingTestTarget(t *testing.T) {
	catalog := Catalog{
		Version: 1,
		Standards: []Standard{{
			ID: "fixture-standard", Name: "Fixture", Version: "1", Category: "protocol", Surface: "edge",
			Status: StatusSupported, Scope: "scope", Limitations: "limits", DocsURL: "https://example.com/fixture", Evidence: "evidence",
			Conformance: []ConformanceRef{{Fixture: "fixture-test", Tier: ConformanceUnit, Target: "pkg/missing_test.go::TestMissing"}},
		}},
	}
	if err := CheckConformance(catalog, t.TempDir()); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("CheckConformance error = %v, want missing target error", err)
	}
}
