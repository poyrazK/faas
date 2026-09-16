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

func TestReservedAppSlugs(t *testing.T) {
	for _, slug := range []string{
		"account", "admin", "api", "assets", "billing", "cdn", "console",
		"dashboard", "docs", "help", "login", "logout", "mail", "ns",
		"operations", "security", "signup", "static", "status", "support", "www",
	} {
		// "ns" is reserved even though it is shorter than the public slug
		// grammar, so callers can return the specific platform-reservation
		// reason before the generic shape error.
		if slug != "ns" && !ValidAppSlug(slug) {
			t.Errorf("reserved slug %q must remain syntactically valid for existing-row access", slug)
		}
		if !IsReservedAppSlug(slug) {
			t.Errorf("IsReservedAppSlug(%q) = false", slug)
		}
	}
	for _, slug := range []string{"status-page", "my-admin", "customer-api"} {
		if IsReservedAppSlug(slug) {
			t.Errorf("near-miss slug %q was reserved", slug)
		}
	}
}
