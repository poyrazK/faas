package imaged

// adr: 435

// ADR-435: exercise the production RegistryClient and real P256 verifier as
// one deploy admission path. A signed index covers only its digest-verified
// selected child; mutable tags and registry headers cannot substitute content.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
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
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func signatureRegistryJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func signatureRegistryDigest(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }

func TestPrepareContainerImageRealSignedIndexRegistry(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// A valid empty tar layer is declared but never fetched by this preparation
	// gate. Executable layer reads belong to the subsequent conversion path.
	tar := make([]byte, 1024)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(tar); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	config := signatureRegistryJSON(t, map[string]any{"os": "linux", "architecture": "amd64", "config": map[string]any{"Cmd": []string{"/app/start"}}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{signatureRegistryDigest(tar)}}})
	child := signatureRegistryJSON(t, map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": oci.Descriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: signatureRegistryDigest(config), Size: int64(len(config))}, "layers": []oci.Descriptor{{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: signatureRegistryDigest(compressed.Bytes()), Size: int64(compressed.Len())}}})
	childDigest := signatureRegistryDigest(child)
	index := signatureRegistryJSON(t, map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": []any{map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": childDigest, "size": len(child), "platform": map[string]string{"os": "linux", "architecture": "amd64"}}}})
	indexDigest := signatureRegistryDigest(index)
	for _, mode := range []string{"approved index", "signed child only", "unapproved key", "payload substitution", "source substitution", "child substitution"} {
		t.Run(mode, func(t *testing.T) {
			th := newTestHarness(t, state.DeploymentKindImage, "pro", "")
			th.app.SecurityPolicy = api.AppSecurityPolicyEnforce
			th.dep.ImageDigest = "registry.example/team/service:latest"
			th.createReplacementDeployment(t)
			keyDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := th.store.UpsertAppTrustedSigner(t.Context(), th.app.AccountID, th.app.ID, "company", keyDER, th.app.AccountID); err != nil {
				t.Fatal(err)
			}

			signedDigest := indexDigest
			if mode == "signed child only" {
				signedDigest = childDigest
			}
			payload := signatureRegistryJSON(t, map[string]any{"critical": map[string]any{"identity": map[string]string{"docker-reference": "registry.example/team/service"}, "image": map[string]string{"docker-manifest-digest": signedDigest}, "type": "cosign container image signature"}})
			hash := sha256.Sum256(payload)
			signer := key
			if mode == "unapproved key" {
				signer = other
			}
			sig, err := ecdsa.SignASN1(rand.Reader, signer, hash[:])
			if err != nil {
				t.Fatal(err)
			}
			attachment := signatureRegistryJSON(t, map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": oci.Descriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: signatureRegistryDigest([]byte("{}")), Size: 2}, "layers": []any{map[string]any{"mediaType": oci.SimpleSigningMediaType, "digest": signatureRegistryDigest(payload), "size": len(payload), "annotations": map[string]string{"dev.cosignproject.cosign/signature": base64.StdEncoding.EncodeToString(sig)}}}})
			auth := &oci.BasicAuth{Username: "company-build", Password: "private-password"}
			var mu sync.Mutex
			tagReads, attachmentReads, tokenCalls := 0, 0, 0
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					u, p, ok := r.BasicAuth()
					if !ok || u != auth.Username || p != auth.Password {
						http.Error(w, "unauthorized", 401)
						return
					}
					mu.Lock()
					tokenCalls++
					mu.Unlock()
					_, _ = w.Write([]byte(`{"token":"private-token"}`))
					return
				}
				if r.Header.Get("Authorization") != "Bearer private-token" {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="registry",scope="repository:team/service:pull"`, srv.URL))
					w.WriteHeader(401)
					return
				}
				switch r.URL.Path {
				case "/v2/team/service/manifests/latest":
					mu.Lock()
					tagReads++
					reads := tagReads
					mu.Unlock()
					if reads > 1 {
						t.Error("mutable image tag read again")
						http.Error(w, "changed tag", 409)
						return
					}
					w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
					w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("f", 64))
					_, _ = w.Write(index)
				case "/v2/team/service/manifests/" + indexDigest:
					w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
					if mode == "source substitution" {
						_, _ = w.Write(child)
					} else {
						_, _ = w.Write(index)
					}
				case "/v2/team/service/manifests/" + childDigest:
					w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
					if mode == "child substitution" {
						_, _ = w.Write(index)
					} else {
						_, _ = w.Write(child)
					}
				case "/v2/team/service/blobs/" + signatureRegistryDigest(config):
					_, _ = w.Write(config)
				case "/v2/team/service/manifests/sha256-" + strings.TrimPrefix(indexDigest, "sha256:") + ".sig":
					mu.Lock()
					attachmentReads++
					mu.Unlock()
					w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
					_, _ = w.Write(attachment)
				case "/v2/team/service/blobs/" + signatureRegistryDigest(payload):
					if mode == "payload substitution" {
						_, _ = w.Write([]byte("substituted signature payload"))
					} else {
						_, _ = w.Write(payload)
					}
				default:
					t.Errorf("unexpected image/signature blob request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			client := oci.NewRegistryClient(oci.WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")), oci.WithHTTPClient(srv.Client()), oci.WithPullRetry(0, 0))
			h := New(th.store, th.notif, client, th.bld, "./init", th.appsR, silentLogger())
			h.trustedPublishersCacheOK = true
			h.trustedPublishersCache = map[string][]cosign.TrustedPublisher{th.app.ID: {{Name: "company", PublicKey: &key.PublicKey}}}
			ref, digest, err := h.prepareContainerImage(context.Background(), th.app, th.dep, auth)
			if mode == "approved index" {
				if err != nil || ref != "registry.example/team/service@"+childDigest || digest != childDigest {
					t.Fatalf("signed source lost selected child: %q %q %v", ref, digest, err)
				}
			} else {
				if err == nil || ref != "" || digest != "" {
					t.Fatalf("accepted substituted/unapproved content: %q %q %v", ref, digest, err)
				}
				if mode == "child substitution" {
					if !errors.Is(err, oci.ErrImageManifestInvalid) {
						t.Fatalf("child content failure lost: %v", err)
					}
				} else if !errors.Is(err, cosign.ErrSignatureInvalid) {
					t.Fatalf("publisher verification failure lost: %v", err)
				}
				dep, err := th.store.DeploymentByID(context.Background(), th.dep.ID)
				if err != nil || dep.Status != state.DeployFailed {
					t.Fatalf("failed gate not persisted: %+v %v", dep, err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if tagReads != 1 || tokenCalls == 0 {
				t.Fatalf("tag/auth behavior: tag=%d token=%d", tagReads, tokenCalls)
			}
			if (mode == "source substitution" || mode == "child substitution") && attachmentReads != 0 {
				t.Fatal("signature lookup before source chain verified")
			}
			if mode != "source substitution" && mode != "child substitution" && attachmentReads != 1 {
				t.Fatalf("publisher gate not exercised: %d", attachmentReads)
			}
			if th.dep.ImageDigest != "registry.example/team/service:latest" {
				t.Fatal("customer reference overwritten")
			}
		})
	}
}
