package oci

// ADR-393: attachment transport obeys registry content addressing, bounds and
// repository-scoped authentication before any publisher claim can be trusted.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func signatureTestManifest(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	return map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": imageContentDigest([]byte("{}")), "size": 2},
		"layers": []any{map[string]any{"mediaType": SimpleSigningMediaType, "digest": imageContentDigest(payload), "size": len(payload),
			"annotations": map[string]any{imageSignatureAnnotation: base64.StdEncoding.EncodeToString([]byte("detached DER bytes"))}}}}
}
func signatureTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestImageSignatureAttachmentsUseIndependentContentAddressingAndAuth(t *testing.T) {
	payload := []byte(`{"critical":{"image":{"docker-manifest-digest":"subject"}}}`)
	manifest := signatureTestJSON(t, signatureTestManifest(t, payload))
	digest := "sha256:" + strings.Repeat("a", 64)
	auth := &BasicAuth{Username: "company-ci", Password: "secret-private-password"}
	var mu sync.Mutex
	var authenticated []string
	var tokenCalls int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != auth.Username || pass != auth.Password {
				http.Error(w, "unauthorized", 401)
				return
			}
			if r.URL.Query().Get("scope") != "repository:team/service:pull" {
				t.Error("credential scope widened")
			}
			mu.Lock()
			tokenCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"signature-token"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer signature-token" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="registry",scope="repository:team/service:pull"`, srv.URL))
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		authenticated = append(authenticated, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/v2/team/service/manifests/sha256-" + strings.Repeat("a", 64) + ".sig":
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json; charset=utf-8")
			// A registry header cannot substitute for hashing the actual object.
			w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("b", 64))
			_, _ = w.Write(manifest)
		case "/v2/team/service/blobs/" + imageContentDigest(payload):
			_, _ = w.Write(payload)
		default:
			t.Errorf("unexpected registry request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")), WithHTTPClient(srv.Client()), WithPullRetry(0, 0))
	got, err := client.PullImageSignatureAttachments(context.Background(), "registry.example/team/service@"+digest, digest, auth)
	if err != nil || len(got) != 1 {
		t.Fatalf("attachments: %+v %v", got, err)
	}
	if got[0].ManifestDigest != imageContentDigest(manifest) || got[0].PayloadDigest != imageContentDigest(payload) || string(got[0].Payload) != string(payload) || string(got[0].Signature) != "detached DER bytes" {
		t.Fatalf("unverified transport data: %+v", got[0])
	}
	mu.Lock()
	defer mu.Unlock()
	if tokenCalls == 0 || len(authenticated) != 2 {
		t.Fatalf("private attachment path not authenticated: %v tokens=%d", authenticated, tokenCalls)
	}
}

func TestImageSignatureAttachmentsRejectInvalidTransportContent(t *testing.T) {
	payload := []byte(`{"claim":"content"}`)
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name        string
		mutate      func(map[string]any)
		blob        []byte
		contentType string
		raw         []byte
	}{
		{"payload digest mismatch", nil, []byte("substituted"), "", nil},
		{"payload size mismatch", func(m map[string]any) { m["layers"].([]any)[0].(map[string]any)["size"] = len(payload) + 1 }, nil, "", nil},
		{"payload truncated", nil, payload[:5], "", nil},
		{"payload over limit", nil, make([]byte, api.ImageSignatureMaxPayloadBytes+1), "", nil},
		{"descriptor over limit", func(m map[string]any) {
			m["layers"].([]any)[0].(map[string]any)["size"] = api.ImageSignatureMaxPayloadBytes + 1
		}, nil, "", nil},
		{"invalid descriptor digest", func(m map[string]any) { m["layers"].([]any)[0].(map[string]any)["digest"] = "sha256:ABC" }, nil, "", nil},
		{"unknown payload type", func(m map[string]any) {
			m["layers"].([]any)[0].(map[string]any)["mediaType"] = "application/octet-stream"
		}, nil, "", nil},
		{"annotation absent", func(m map[string]any) { delete(m["layers"].([]any)[0].(map[string]any), "annotations") }, nil, "", nil},
		{"annotation invalid base64", func(m map[string]any) {
			m["layers"].([]any)[0].(map[string]any)["annotations"] = map[string]string{imageSignatureAnnotation: "%%%"}
		}, nil, "", nil},
		{"annotation over limit", func(m map[string]any) {
			m["layers"].([]any)[0].(map[string]any)["annotations"] = map[string]string{imageSignatureAnnotation: base64.StdEncoding.EncodeToString(make([]byte, api.ImageSignatureMaxDERBytes+1))}
		}, nil, "", nil},
		{"no signatures", func(m map[string]any) { m["layers"] = []any{} }, nil, "", nil},
		{"too many signatures", func(m map[string]any) {
			layer := m["layers"].([]any)[0]
			layers := make([]any, api.ImageSignatureMaxEntries+1)
			for i := range layers {
				layers[i] = layer
			}
			m["layers"] = layers
		}, nil, "", nil},
		{"wrong schema", func(m map[string]any) { m["schemaVersion"] = 1 }, nil, "", nil},
		{"invalid config", func(m map[string]any) { m["config"].(map[string]any)["digest"] = "invalid" }, nil, "", nil},
		{"content type mismatch", nil, nil, "application/vnd.docker.distribution.manifest.v2+json", nil},
		{"index format", func(m map[string]any) { m["mediaType"] = "application/vnd.oci.image.index.v1+json" }, nil, "application/vnd.oci.image.index.v1+json", nil},
		{"invalid JSON", nil, nil, "", []byte("{")},
		{"manifest over limit", nil, nil, "", make([]byte, api.ImageSignatureMaxManifestBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := signatureTestManifest(t, payload)
			if tc.mutate != nil {
				tc.mutate(m)
			}
			body := signatureTestJSON(t, m)
			if tc.raw != nil {
				body = tc.raw
			}
			blob := payload
			if tc.blob != nil {
				blob = tc.blob
			}
			ct := tc.contentType
			if ct == "" {
				ct = "application/vnd.oci.image.manifest.v1+json"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/manifests/") {
					w.Header().Set("Content-Type", ct)
					_, _ = w.Write(body)
					return
				}
				if strings.Contains(r.URL.Path, "/blobs/") {
					_, _ = w.Write(blob)
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()
			client := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")), WithPullRetry(0, 0))
			got, err := client.PullImageSignatureAttachments(context.Background(), "registry.example/team/service@"+digest, digest, nil)
			if !errors.Is(err, ErrImageManifestInvalid) || len(got) != 0 {
				t.Fatalf("invalid transport accepted: %+v %v", got, err)
			}
		})
	}
}

func TestImageSignatureAttachmentMissingIsOnlyAttachment404(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	payload := []byte("claim")
	body := signatureTestJSON(t, signatureTestManifest(t, payload))
	auth := &BasicAuth{Username: "credential-user", Password: "secret-password"}
	for _, tc := range []struct {
		name    string
		status  int
		blob    bool
		missing bool
	}{
		{"attachment missing", 404, false, true}, {"forbidden", 403, false, false}, {"unauthorized", 401, false, false}, {"server failure", 500, false, false}, {"payload missing", 404, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.blob && strings.Contains(r.URL.Path, "/manifests/") {
					w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
					_, _ = w.Write(body)
					return
				}
				http.Error(w, auth.Username+" "+auth.Password, tc.status)
			}))
			defer srv.Close()
			client := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")), WithPullRetry(0, 0))
			got, err := client.PullImageSignatureAttachments(context.Background(), "registry.example/team/service@"+digest, digest, auth)
			if err == nil || len(got) != 0 || errors.Is(err, ErrImageSignatureMissing) != tc.missing {
				t.Fatalf("status classification: %+v %v", got, err)
			}
			if strings.Contains(err.Error(), auth.Username) || strings.Contains(err.Error(), auth.Password) {
				t.Fatal("registry error leaked credentials")
			}
		})
	}
}

func TestImageSignatureAttachmentsRejectScopeBeforeNetwork(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; http.NotFound(w, r) }))
	defer srv.Close()
	client := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")))
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, subject := range []string{"sha256:ABC", "sha256:" + strings.Repeat("b", 64)} {
		if _, err := client.PullImageSignatureAttachments(context.Background(), "registry.example/team/service@"+digest, subject, nil); !errors.Is(err, ErrImageManifestInvalid) {
			t.Fatalf("scope mismatch: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.PullImageSignatureAttachments(ctx, "registry.example/team/service@"+digest, digest, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if requests != 0 {
		t.Fatalf("invalid authority reached registry: %d", requests)
	}
}
