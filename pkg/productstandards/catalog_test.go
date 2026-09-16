package productstandards

import (
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
			{ID: "z-last", Name: "Z", Version: "1", Category: "protocol", Surface: "edge", Status: StatusSupported, Scope: "z", Limitations: "z", DocsURL: "https://example.com/z", Evidence: "z"},
			{ID: "a-first", Name: "A", Version: "1", Category: "protocol", Surface: "edge", Status: StatusSupported, Scope: "a", Limitations: "a", DocsURL: "https://example.com/a", Evidence: "a"},
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
