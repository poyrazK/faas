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

// H77: the HEAD check proves nothing against a registry the requester runs,
// which can answer 200 for any digest. A blob pulled with credentials stays
// under its registry and repository, so a lying registry that lists another
// tenant's private digest gets a cache miss and must serve the bytes itself.
func TestPrivatelyPulledBlobIsNotServedToALyingRegistry(t *testing.T) {
	secret := []byte("victim's private layer")
	sum := sha256.Sum256(secret)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	victim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/victim/private/blobs/"+digest {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(secret)
		}
	}))
	t.Cleanup(victim.Close)
	liarGets := 0
	liar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Claims every digest and has none of the bytes.
		if r.Method == http.MethodGet {
			liarGets++
			_, _ = w.Write([]byte("not the victim's bytes"))
		}
	}))
	t.Cleanup(liar.Close)

	cache, err := NewDiskBlobCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	victimURL, _ := url.Parse(victim.URL)
	liarURL, _ := url.Parse(liar.URL)
	read := func(host, repository string, auth *BasicAuth) ([]byte, error) {
		c := NewRegistryClient(WithEndpoint("http", host), WithBlobCache(cache))
		_, rc, err := c.openBlobWithAuth(t.Context(), Reference{Registry: host, Repository: repository}, digest, auth)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(rc)
	}

	creds := &BasicAuth{Username: "victim", Password: "registry-token"}
	if got, err := read(victimURL.Host, "victim/private", creds); err != nil || string(got) != string(secret) {
		t.Fatalf("victim's credentialed pull = %q, %v", got, err)
	}
	for _, auth := range []*BasicAuth{nil, {Username: "attacker", Password: "x"}} {
		got, err := read(liarURL.Host, "attacker/anything", auth)
		if err == nil && string(got) == string(secret) {
			t.Fatalf("lying registry (auth=%v) read the victim's private blob from the cache", auth != nil)
		}
	}
	if liarGets == 0 {
		t.Fatal("the lying registry's pulls were served from the cache instead of missing it")
	}
	// The victim's own repeat pull still hits its scoped entry.
	if got, err := read(victimURL.Host, "victim/private", creds); err != nil || string(got) != string(secret) {
		t.Fatalf("victim's repeat pull = %q, %v", got, err)
	}
}

// An anonymous (public) pull fills the shared pool, and a credentialed pull
// of the same digest from another repository may read that public copy.
func TestCredentialedPullReadsASharedPublicCopy(t *testing.T) {
	public := []byte("public base layer")
	sum := sha256.Sum256(public)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
			_, _ = w.Write(public)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	cache, err := NewDiskBlobCache(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	c := NewRegistryClient(WithEndpoint("http", u.Host), WithBlobCache(cache))
	for _, step := range []struct {
		repo string
		auth *BasicAuth
	}{{"library/base", nil}, {"team/private", &BasicAuth{Username: "u", Password: "p"}}} {
		_, rc, err := c.openBlobWithAuth(t.Context(), Reference{Registry: u.Host, Repository: step.repo}, digest, step.auth)
		if err != nil {
			t.Fatalf("%s: %v", step.repo, err)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		if string(got) != string(public) {
			t.Fatalf("%s read %q", step.repo, got)
		}
	}
	if gets != 1 {
		t.Fatalf("blob GETs = %d, want 1 (the credentialed pull reads the shared public copy)", gets)
	}
}
