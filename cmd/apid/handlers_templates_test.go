// handlers_templates_test.go — Issue #961 / Mega-B PR-3.
//
// Pins the GET /v1/templates wire shape + auth gate. The dashboard
// hydrates its wizard's <select> from this endpoint; the CLI's
// runtime validator reads the same templates.Names locally. Both
// paths must agree — the deploy doc notes the release pairing.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// TestListTemplates_ReturnsAll15Names asserts every name in
// templates.Names shows up in the response with non-empty category
// + description. Catches a future template added to embed.FS but
// missed in templates.Names (or vice versa) and a description added
// without a CategoryFor entry.
func TestListTemplates_ReturnsAll15Names(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/templates", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	var rows []struct {
		Name        string `json:"name"`
		Category    string `json:"category"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v\nbody = %s", err, rec.Body.String())
	}
	if len(rows) != len(templates.Names) {
		t.Fatalf("len(rows) = %d, want %d", len(rows), len(templates.Names))
	}
	for _, want := range templates.Names {
		var found bool
		for _, r := range rows {
			if r.Name == want {
				found = true
				if r.Category == "" {
					t.Errorf("template %q: empty category", want)
				}
				if r.Description == "" {
					t.Errorf("template %q: empty description", want)
				}
			}
		}
		if !found {
			t.Errorf("template %q missing from /v1/templates response", want)
		}
	}
}

// TestListTemplates_RequiresSessionAuth asserts the endpoint refuses
// unauthenticated requests with 401. The dashboard is the only
// caller today, but the route is /v1/* so a future API consumer
// should not see the catalog anonymously.
func TestListTemplates_RequiresSessionAuth(t *testing.T) {
	srv, _ := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/templates", nil)
	// no cookie
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 401 or 302\nbody = %s", rec.Code, rec.Body.String())
	}
}
