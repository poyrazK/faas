package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestPrintPublicURLsNamesRequiredAuth pins Hunt 32 H32-30: a bearer-auth
// app was listed as "Public on the internet" with no hint that it needs a key.
func TestPrintPublicURLsNamesRequiredAuth(t *testing.T) {
	var out bytes.Buffer
	main.PrintPublicURLs(&out, []api.PlanWorkload{
		{Name: "web", PublicURL: "https://web.example.dev", PublicAuthMode: api.AppPublicAuthModeBearer},
		{Name: "site", PublicURL: "https://site.example.dev"},
	})
	got := out.String()
	if !strings.Contains(got, "https://web.example.dev (needs an API key)") {
		t.Fatalf("bearer app missing its auth note:\n%s", got)
	}
	if strings.Contains(got, "https://site.example.dev (") {
		t.Fatalf("open app got an auth note:\n%s", got)
	}
}
