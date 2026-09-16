package api

import (
	"strings"
	"testing"
)

func TestValidProjectSlug(t *testing.T) {
	tests := map[string]bool{
		"a":                     true,
		"project":               true,
		"project-service-2026":  true,
		strings.Repeat("a", 63): true,
		"":                      false,
		"Bad_Slug":              false,
		"-project":              false,
		"project-":              false,
		strings.Repeat("a", 64): false,
	}
	for slug, want := range tests {
		if got := ValidProjectSlug(slug); got != want {
			t.Errorf("ValidProjectSlug(%q) = %v, want %v", slug, got, want)
		}
	}
}
