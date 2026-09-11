package api

import "testing"

func TestValidAppSlug(t *testing.T) {
	tests := map[string]bool{
		"api":            true,
		"api-service-24": true,
		"API-service":    false,
		"api_service":    false,
		"api.service":    false,
		"ab":             false,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": false,
	}
	for slug, want := range tests {
		if got := ValidAppSlug(slug); got != want {
			t.Errorf("ValidAppSlug(%q) = %v, want %v", slug, got, want)
		}
	}
}
