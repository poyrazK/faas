package api

import "testing"

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
