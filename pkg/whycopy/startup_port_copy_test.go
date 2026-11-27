package whycopy

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// prod 2026-11-19: an app that listened on a hard-coded 8081 failed with
// only "didn't become ready in time"; the first fix names PORT.
func TestStartupTimeoutFixNamesThePortFirst(t *testing.T) {
	fix := catalog[api.CodeAppStartupTimeout].Fix
	first, _, _ := strings.Cut(fix, "\n")
	if !strings.Contains(first, "PORT") || !strings.Contains(first, "hard-coded port") {
		t.Fatalf("first fix bullet = %q", first)
	}
}
