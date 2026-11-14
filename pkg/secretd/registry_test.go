package secretd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"filippo.io/age"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
)

type fetchStream struct {
	grpc.ServerStream
	ctx    context.Context
	frames []*secretdpb.RegistryFetchResponse
}

func (s *fetchStream) Context() context.Context { return s.ctx }

func (s *fetchStream) Send(f *secretdpb.RegistryFetchResponse) error {
	s.frames = append(s.frames, f)
	return nil
}

func (s *fetchStream) result() (int32, string, string) {
	var status int32
	var digest string
	var body strings.Builder
	for _, f := range s.frames {
		if h := f.GetHead(); h != nil {
			status = h.GetStatus()
			for _, kv := range h.GetHeaders() {
				if kv.GetName() == "Docker-Content-Digest" {
					digest = kv.GetValue()
				}
			}
		}
		body.Write(f.GetBodyChunk())
	}
	return status, digest, body.String()
}

// fakeRegistry is a private registry that answers /v2/ requests only with
// a bearer token its realm issues for user:pass, and redirects blobs.
type fakeRegistry struct {
	srv          *httptest.Server
	tokenAsks    atomic.Int32
	sawCallerHdr atomic.Bool
	basicOnly    bool
}

func newFakeRegistry(t *testing.T, basicOnly bool) *fakeRegistry {
	r := &fakeRegistry{basicOnly: basicOnly}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, req *http.Request) {
		user, pass, ok := req.BasicAuth()
		if !ok || user != "robot" || pass != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r.tokenAsks.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok-" + req.URL.Query().Get("scope"), "expires_in": 300})
	})
	mux.HandleFunc("/storage/", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" {
			t.Errorf("storage got an Authorization header")
		}
		_, _ = w.Write([]byte("layer-bytes"))
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.Header.Get("Authorization"), "caller") {
			r.sawCallerHdr.Store(true)
		}
		authorized := req.Header.Get("Authorization") == "Bearer tok-repository:team/app:pull"
		if r.basicOnly {
			user, pass, ok := req.BasicAuth()
			authorized = ok && user == "robot" && pass == "s3cret"
		}
		if !authorized {
			if r.basicOnly {
				w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			} else {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+r.srv.URL+`/token",service="reg",scope="repository:team/app:pull"`)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(req.URL.Path, "/manifests/v1"):
			w.Header().Set("Docker-Content-Digest", "sha256:abc")
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Set-Cookie", "session=leak")
			_, _ = w.Write([]byte(`{"schemaVersion":2}`))
		case strings.Contains(req.URL.Path, "/blobs/"):
			http.Redirect(w, req, "/storage/blob", http.StatusTemporaryRedirect)
		default:
			http.NotFound(w, req)
		}
	})
	r.srv = httptest.NewTLSServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRegistry) host() string { return strings.TrimPrefix(r.srv.URL, "https://") }

// Slice 3j: secretd pulls from an app's private registry with the app's
// password, answering the registry's challenge itself; the caller gets only
// the registry's answer.
func TestRegistryFetch(t *testing.T) {
	for _, basicOnly := range []bool{false, true} {
		reg := newFakeRegistry(t, basicOnly)
		f := newFixture(t, 0)
		build, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := secretbox.SealBytes(build.Recipient(), secretd.RegistryCredentialNamespace, []byte("s3cret"), 0)
		if err != nil {
			t.Fatal(err)
		}
		f.store.registries = map[[2]string]fakeRegistryCredential{{"app-a", reg.host()}: {username: "robot", sealed: sealed}}
		f.server.WithRegistryHTTPClient(reg.srv.Client())
		fetch := func(app, method, url string) (*fetchStream, codes.Code) {
			s := &fetchStream{ctx: f.ctx}
			err := f.server.RegistryFetch(&secretdpb.RegistryFetchRequest{AppId: app, Method: method, Url: url,
				Headers: []*secretdpb.RegistryHeader{{Name: "Authorization", Value: "Bearer caller"}, {Name: "Accept", Value: "*/*"}}}, s)
			return s, status.Code(err)
		}
		manifest := reg.srv.URL + "/v2/team/app/manifests/v1"
		if _, code := fetch("app-a", "GET", manifest); code != codes.FailedPrecondition {
			t.Fatalf("without a build identity = %v", code)
		}
		f.server.WithBuildIdentities([]*age.X25519Identity{build})
		for range 2 {
			s, code := fetch("app-a", "GET", manifest)
			status, digest, body := s.result()
			if code != codes.OK || status != 200 || digest != "sha256:abc" || body != `{"schemaVersion":2}` {
				t.Fatalf("basic=%v manifest = %v %d %q %q", basicOnly, code, status, digest, body)
			}
			for _, fr := range s.frames {
				for _, h := range fr.GetHead().GetHeaders() {
					if h.GetName() == "Set-Cookie" {
						t.Fatal("a registry Set-Cookie reached the caller")
					}
				}
			}
		}
		if !basicOnly && reg.tokenAsks.Load() != 1 {
			t.Fatalf("token asks = %d, want 1 (cached)", reg.tokenAsks.Load())
		}
		if reg.sawCallerHdr.Load() {
			t.Fatal("the caller's Authorization header reached the registry")
		}
		if f.store.opens[len(f.store.opens)-1] != "n-owner/app-a/owner" {
			t.Fatalf("audit = %v", f.store.opens)
		}
		s, code := fetch("app-a", "GET", reg.srv.URL+"/v2/team/app/blobs/sha256:def")
		if status, _, body := s.result(); code != codes.OK || status != 200 || body != "layer-bytes" {
			t.Fatalf("blob through redirect = %v %d %q", code, status, body)
		}
		for name, tc := range map[string]struct {
			app, method, url string
			want             codes.Code
		}{
			"POST":               {"app-a", "POST", manifest, codes.InvalidArgument},
			"plain http":         {"app-a", "GET", strings.Replace(manifest, "https://", "http://", 1), codes.InvalidArgument},
			"outside /v2/":       {"app-a", "GET", reg.srv.URL + "/token", codes.InvalidArgument},
			"user info":          {"app-a", "GET", "https://u:p@" + reg.host() + "/v2/team/app/manifests/v1", codes.InvalidArgument},
			"unknown registry":   {"app-a", "GET", "https://registry.example.com/v2/team/app/manifests/v1", codes.NotFound},
			"another node's app": {"app-b", "GET", manifest, codes.PermissionDenied},
			"path traversal":     {"app-a", "GET", reg.srv.URL + "/v2/../token", codes.InvalidArgument},
		} {
			if _, code := fetch(tc.app, tc.method, tc.url); code != tc.want {
				t.Errorf("basic=%v %s = %v, want %v", basicOnly, name, code, tc.want)
			}
		}
	}
}
