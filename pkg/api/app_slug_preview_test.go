package api

import (
	"strings"
	"testing"
)

// Ordinary allocation paths reject the preview slug shape so another app
// cannot squat a future PR preview's slug and hostname.
func TestPreviewAppSlugIsReserved(t *testing.T) {
	for slug, want := range map[string]bool{
		"pr-7-shop":    true,
		"pr-123-a-b-c": true,
		"pr-07-shop":   false,
		"pr-x-shop":    false,
		"pr--shop":     false,
		"pr-7":         false,
		"print-shop":   false,
		"shop":         false,
	} {
		if got := IsReservedAppSlug(slug); got != want {
			t.Errorf("IsReservedAppSlug(%q) = %v, want %v", slug, got, want)
		}
	}
}

// A PR preview of a 40-character app is longer than ValidAppSlug allows, so
// preview slugs are valid up to one DNS label when their parent is valid.
func TestValidPreviewAppSlug(t *testing.T) {
	parent40 := "a" + strings.Repeat("b", 38) + "c"
	for slug, want := range map[string]bool{
		"pr-7-shop":                           true,
		"pr-7-" + parent40:                    true,
		"pr-123456-" + parent40:               true,
		"pr-1234567890123456789-" + parent40:  true, // exactly 63 characters
		"pr-12345678901234567890-" + parent40: false,
		"pr-7-" + parent40 + "d":              false,
		"pr-7-Shop":                           false,
		"pr-7-s":                              false,
		"pr-07-shop":                          false,
		"shop":                                false,
	} {
		if got := ValidPreviewAppSlug(slug); got != want {
			t.Errorf("ValidPreviewAppSlug(%q) = %v, want %v", slug, got, want)
		}
	}
}
