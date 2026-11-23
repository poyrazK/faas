package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r26: `app get` of an internal-only app printed its
// <slug>.gregale.app URL, which answers 404 on the public edge.
func TestAppGetInternalAppPrintsNoPublicURL(t *testing.T) {
	internal := main.AppGetURL(api.AppResponse{Slug: "api", URL: "https://api.gregale.app", Visibility: "internal"})
	if !strings.HasPrefix(internal, "none (internal") {
		t.Fatalf("internal app url row = %q", internal)
	}
	public := main.AppGetURL(api.AppResponse{Slug: "api", URL: "https://api.gregale.app", Visibility: "public"})
	if public != "https://api.gregale.app" {
		t.Fatalf("public app url row = %q", public)
	}
}
