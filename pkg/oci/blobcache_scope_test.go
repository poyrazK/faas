package oci

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The blob cache shares bytes by digest across every pull on a node. A pull
// reads a cached copy only after its own repository is shown to hold the
// blob: otherwise a tenant who learned another tenant's private layer
// digests could list them in a manifest of its own and read them from the
// cache without ever authenticating to the registry that holds them.
func TestCachedBlobIsServedOnlyToRepositoriesThatHoldIt(t *testing.T) {
	secret := []byte("private layer bytes")
	sum := sha256.Sum256(secret)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	gets := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/victim/private/blobs/"+digest, func(w http.ResponseWriter, r *http.Request) {
		gets[r.Method]++
		if r.Method == http.MethodGet {
			_, _ = w.Write(secret)
		}
	})
	// A repository whose registry hands the blob to storage: the redirect
	// target is signed for GET only, so the check must not follow it.
	mux.HandleFunc("/v2/mirror/redirected/blobs/"+digest, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/storage/"+digest, http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/storage/"+digest, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "signed for GET", http.StatusForbidden)
			return
		}
		_, _ = w.Write(secret)
	})
	mux.HandleFunc("/v2/attacker/public/blobs/", http.NotFound)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	cache, err := NewDiskBlobCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	c := NewRegistryClient(WithEndpoint("http", u.Host), WithBlobCache(cache))
	read := func(repository string) ([]byte, error) {
		_, rc, err := c.openBlobWithAuth(t.Context(), Reference{Registry: u.Host, Repository: repository}, digest, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(rc)
	}

	if got, err := read("victim/private"); err != nil || string(got) != string(secret) {
		t.Fatalf("owner's pull = %q, %v", got, err)
	}
	if got, err := read("attacker/public"); !errors.Is(err, ErrImageNotFound) || got != nil {
		t.Fatalf("pull from a repository without the blob = %q, %v; want ErrImageNotFound", got, err)
	}
	if got, err := read("mirror/redirected"); err != nil || string(got) != string(secret) {
		t.Fatalf("pull from a repository that redirects to storage = %q, %v", got, err)
	}
	// A confirmed repository reads the cache without asking again.
	before := gets[http.MethodHead]
	if _, err := read("victim/private"); err != nil || gets[http.MethodHead] != before || gets[http.MethodGet] != 1 {
		t.Fatalf("second owner pull: err=%v HEAD %d→%d GET %d", err, before, gets[http.MethodHead], gets[http.MethodGet])
	}
}
