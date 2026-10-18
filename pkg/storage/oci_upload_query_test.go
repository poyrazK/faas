// adr: 025
package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOCIUploadCompletesWithRegistryQueryState(t *testing.T) {
	const payload = "artifact body"
	digestHex := sha256Hex(payload)
	digest := "sha256:" + digestHex
	for _, tc := range []struct {
		name     string
		location string
		query    string
	}{
		{"digest in path", "/uploads/digest=session", "digest=" + digest},
		{"digest in another key", "/uploads/session?upload_digest=pending", "upload_digest=pending&digest=" + digest},
		{"digest in state value", "/uploads/session?state=digest=pending", "state=digest=pending&digest=" + digest},
		{"fragment", "/uploads/session#resume", "digest=" + digest},
		{"fragment after state", "/uploads/session?state=a%2fb+%20#resume?ignored=yes", "state=a%2fb+%20&digest=" + digest},
		{"existing digest", "/uploads/session?digest=" + digest, "digest=" + digest},
		{"encoded digest key", "/uploads/session?%64igest=sha256%3A" + digestHex, "%64igest=sha256%3A" + digestHex},
		{"opaque query order", "/uploads/session?z=last&a=first&state=a%2fb+%20&z=other", "z=last&a=first&state=a%2fb+%20&z=other&digest=" + digest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts, puts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != payload {
					t.Errorf("%s body = %q, %v; want %q", r.Method, body, err, payload)
					http.Error(w, "invalid artifact body", http.StatusBadRequest)
					return
				}
				switch r.Method {
				case http.MethodPost:
					posts.Add(1)
					w.Header().Set("Location", tc.location)
					w.WriteHeader(http.StatusAccepted)
				case http.MethodPut:
					puts.Add(1)
					digests := r.URL.Query()["digest"]
					if len(digests) != 1 || digests[0] != digest {
						t.Errorf("commit digests = %v, want [%s]", digests, digest)
						http.Error(w, "missing or duplicate digest", http.StatusBadRequest)
						return
					}
					if r.URL.RawQuery != tc.query {
						t.Errorf("registry query = %q, want %q", r.URL.RawQuery, tc.query)
					}
					w.WriteHeader(http.StatusCreated)
				default:
					http.Error(w, "unexpected request", http.StatusMethodNotAllowed)
				}
			}))
			t.Cleanup(server.Close)
			backend, err := NewOCIRegistryStorageBackend(WithRegistry(server.URL), WithHTTPClient(server.Client()))
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.pushBlobMonolithic(t.Context(), "app", digestHex, strings.NewReader(payload)); err != nil {
				t.Fatalf("upload did not commit: %v", err)
			}
			if posts.Load() != 1 || puts.Load() != 1 {
				t.Errorf("upload requests = POST %d, PUT %d; want one each", posts.Load(), puts.Load())
			}
		})
	}
}
