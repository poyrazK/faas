package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeRegistry is an httptest registry that implements just enough of the v2
// API to exercise the anonymous-token flow and digest resolution.
type fakeRegistry struct {
	srv          *httptest.Server
	token        string
	manifestBody []byte
	digestHeader string // when "", the server omits Docker-Content-Digest
	requireToken bool
	tokenHits    int
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{token: "tok-abc", manifestBody: []byte(`{"schemaVersion":2}`), requireToken: true}
	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenHits++
		// A public pull needs a service+scope but no credentials.
		if r.URL.Query().Get("scope") == "" {
			http.Error(w, "missing scope", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `{"token":%q}`, f.token)
	})

	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/manifests/") {
			http.NotFound(w, r)
			return
		}
		if f.requireToken && r.Header.Get("Authorization") != "Bearer "+f.token {
			w.Header().Set("Www-Authenticate",
				fmt.Sprintf(`Bearer realm=%q,service="registry",scope="repository:org/app:pull"`,
					f.srv.URL+"/token"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.digestHeader != "" {
			w.Header().Set("Docker-Content-Digest", f.digestHeader)
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write(f.manifestBody)
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// client returns a RegistryClient pinned at the fake registry.
func (f *fakeRegistry) client() *RegistryClient {
	u, _ := url.Parse(f.srv.URL)
	return NewRegistryClient(WithEndpoint("http", u.Host))
}

func TestRegistryPullDigest_TokenFlowAndHeader(t *testing.T) {
	f := newFakeRegistry(t)
	f.digestHeader = "sha256:" + hex64
	got, err := f.client().PullDigest(context.Background(), "ghcr.io/org/app:main")
	if err != nil {
		t.Fatalf("PullDigest: %v", err)
	}
	if got != "sha256:"+hex64 {
		t.Errorf("digest = %q, want header value", got)
	}
	if f.tokenHits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", f.tokenHits)
	}
}

func TestRegistryPullDigest_ComputesWhenHeaderAbsent(t *testing.T) {
	f := newFakeRegistry(t)
	f.digestHeader = "" // force the client to hash the body itself
	sum := sha256.Sum256(f.manifestBody)
	want := "sha256:" + hex.EncodeToString(sum[:])

	got, err := f.client().PullDigest(context.Background(), "ghcr.io/org/app:main")
	if err != nil {
		t.Fatalf("PullDigest: %v", err)
	}
	if got != want {
		t.Errorf("digest = %q, want computed %q", got, want)
	}
}

func TestRegistryPullDigest_NotFound(t *testing.T) {
	f := newFakeRegistry(t)
	f.requireToken = false
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unknown manifest", http.StatusNotFound)
	})
	_, err := f.client().PullDigest(context.Background(), "ghcr.io/org/missing:tag")
	if err == nil {
		t.Fatal("expected error for 404 manifest")
	}
}

func TestRegistryPullDigest_BadReference(t *testing.T) {
	c := NewRegistryClient()
	if _, err := c.PullDigest(context.Background(), "app@sha256:short"); err == nil {
		t.Fatal("expected parse error for bad digest")
	}
}

func TestParseChallenge(t *testing.T) {
	ch := parseChallenge(`Bearer realm="https://auth.example/token",service="registry",scope="repository:org/app:pull,push"`)
	if ch.realm != "https://auth.example/token" {
		t.Errorf("realm = %q", ch.realm)
	}
	if ch.service != "registry" {
		t.Errorf("service = %q", ch.service)
	}
	// The scope contains a comma; it must not be split into two params.
	if ch.scope != "repository:org/app:pull,push" {
		t.Errorf("scope = %q", ch.scope)
	}
}

var _ Puller = (*RegistryClient)(nil)
