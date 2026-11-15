package oci

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// public.ecr.aws answers a blob HEAD with 401 even with a valid token, and the
// same GET with a 307 to its CDN. The repository check retries refused HEADs
// as a GET without following the redirect (2026-11-15 agent eval: every public
// ECR image failed admission with "blob … is not served by … (401)").
func TestBlobCheckFallsBackToGetWhenHeadIsRefused(t *testing.T) {
	const digest = "sha256:e437193896d8b7c122308dac597e6abea146d6b17166ed146dca4a498404b292"
	var srv *httptest.Server
	followed := false
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token/":
			_, _ = w.Write([]byte(`{"token":"anon"}`))
		case strings.HasPrefix(r.URL.Path, "/cdn/"):
			followed = true
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			authorized := r.Header.Get("Authorization") == "Bearer anon"
			if !authorized || r.Method == http.MethodHead {
				w.Header().Set("Www-Authenticate", `Bearer realm="`+srv.URL+`/token/",service="public.ecr.aws",scope="aws"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v2/aws-containers/orders/") {
				http.Redirect(w, r, "/cdn/blob", http.StatusTemporaryRedirect)
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c := NewRegistryClient(WithEndpoint("http", u.Host))

	if err := c.confirmBlobInRepository(t.Context(), Reference{Registry: u.Host, Repository: "aws-containers/orders"}, digest, nil); err != nil {
		t.Fatalf("a blob the repository serves over GET was refused: %v", err)
	}
	if followed {
		t.Fatal("the check followed the storage redirect")
	}
	err := c.confirmBlobInRepository(t.Context(), Reference{Registry: u.Host, Repository: "attacker/other"}, digest, nil)
	if !errors.Is(err, ErrImageNotFound) {
		t.Fatalf("a repository that does not serve the blob = %v, want ErrImageNotFound", err)
	}
}
