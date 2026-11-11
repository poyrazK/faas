package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestOrgCreateTakesThePositionalSlug pins Hunt 32 H32-6: `org create acme
// --name "Acme"` reported the slug and the flags as unexpected positional
// arguments.
func TestOrgCreateTakesThePositionalSlug(t *testing.T) {
	var created api.CreateOrgRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&created)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"slug":"acme","plan":"free"}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	_, _, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"org", "create", "acme", "--name", "Acme Inc"}); code != 0 {
		t.Fatalf("org create exit = %d", code)
	}
	if created.Slug != "acme" || created.Name != "Acme Inc" {
		t.Fatalf("create request = %+v", created)
	}
}
