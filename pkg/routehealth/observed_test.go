package routehealth_test

// adr: 20261028110044

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/routehealth"
)

func TestUnobservedWarnings(t *testing.T) {
	labels := []string{routehealth.RouteLabel("POST", "/checkout"), "GET /users/{id}", "GET /users/{id}", "GET /health"}
	got := routehealth.UnobservedWarnings(labels, []string{"POST /checkout"}, 7*24*time.Hour)
	want := []string{
		"GET /users/{id}: no requests matched this selector in the last 7 days; selectors match the route label exactly",
		"GET /health: no requests matched this selector in the last 7 days; selectors match the route label exactly",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("warnings = %q, want %q", got, want)
	}
	if got := routehealth.UnobservedWarnings([]string{"GET /a"}, nil, 12*time.Hour); len(got) != 1 || got[0] != "GET /a: no requests matched this selector in the last 1 day; selectors match the route label exactly" {
		t.Fatalf("short lookback warning = %q", got)
	}
	if got := routehealth.UnobservedWarnings([]string{"GET /a"}, []string{"GET /a"}, time.Hour); got != nil {
		t.Fatalf("observed selector warned: %q", got)
	}
}
