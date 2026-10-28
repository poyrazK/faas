package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261026145832: capabilities lists Gregale's features and the early
// access you can join in one place.
func TestCapabilitiesListsEarlyAccess(t *testing.T) {
	capabilities, err := api.CapabilitiesForPlan(api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(capabilities)
		case "/v1/previews", "/v1/orgs/acme/previews":
			_, _ = fmt.Fprint(w, `{"items":[{"key":"change_timeline","label":"Change timeline","eligible":true},{"key":"realtime","label":"Realtime","joined":true}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	code, stdout, stderr := runCLI(t, "capabilities")
	if code != 0 || !strings.Contains(stdout, "custom-domains") || !strings.Contains(stdout, "EARLY ACCESS") ||
		!strings.Contains(stdout, "change_timeline") || !strings.Contains(stdout, "joined") || !strings.Contains(stdout, "gregale feature join <key>") {
		t.Fatalf("capabilities = %d\n%s%s", code, stdout, stderr)
	}

	paths = nil
	code, stdout, stderr = runCLI(t, "capabilities", "--early-access", "--org", "acme")
	if code != 0 || strings.Contains(stdout, "custom-domains") || !strings.Contains(stdout, "change_timeline") || strings.Join(paths, " ") != "/v1/orgs/acme/previews" {
		t.Fatalf("capabilities --early-access --org acme = %d, paths %v\n%s%s", code, paths, stdout, stderr)
	}

	paths = nil
	code, stdout, _ = runCLI(t, "--json", "capabilities")
	if code != 0 || strings.Contains(stdout, "change_timeline") || strings.Join(paths, " ") != "/v1/capabilities" {
		t.Fatalf("--json capabilities must stay the capabilities document: %d, paths %v\n%s", code, paths, stdout)
	}
}
