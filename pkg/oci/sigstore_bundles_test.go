package oci

// ADR-20261029155852: bundle transport obeys content addressing, names the
// requested subject and stays bounded before cosign sees a byte.

import (
	"context"
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

type bundleTestObject struct {
	contentType string
	body        []byte
}

type bundleTestRegistry struct {
	mu       sync.Mutex
	objects  map[string]bundleTestObject
	requests []string
}

func newBundleTestRegistry(t *testing.T) (*bundleTestRegistry, *RegistryClient) {
	t.Helper()
	reg := &bundleTestRegistry{objects: map[string]bundleTestObject{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		reg.requests = append(reg.requests, r.URL.Path)
		obj, ok := reg.objects[r.URL.Path]
		reg.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if obj.contentType != "" {
			w.Header().Set("Content-Type", obj.contentType)
		}
		_, _ = w.Write(obj.body)
	}))
	t.Cleanup(srv.Close)
	client := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")), WithHTTPClient(srv.Client()), WithPullRetry(0, 0))
	return reg, client
}

func (r *bundleTestRegistry) put(path, contentType string, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.objects[path] = bundleTestObject{contentType: contentType, body: body}
}

func (r *bundleTestRegistry) requested(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.requests {
		if p == path {
			return true
		}
	}
	return false
}

func bundleTestJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

const bundleTestRepo = "/v2/poyrazk/runner-node22"

var bundleTestSubject = "sha256:" + strings.Repeat("d", 64)

// attach publishes one bundle the way attest-build-provenance does: a blob, an
// artifact manifest naming the subject, and returns the manifest descriptor.
func attachTestBundle(t *testing.T, reg *bundleTestRegistry, bundle []byte, mutate func(map[string]any)) map[string]any {
	t.Helper()
	manifest := map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "artifactType": SigstoreBundleMediaType,
		"config":  map[string]any{"mediaType": "application/vnd.oci.empty.v1+json", "digest": imageContentDigest([]byte("{}")), "size": 2},
		"layers":  []any{map[string]any{"mediaType": SigstoreBundleMediaType, "digest": imageContentDigest(bundle), "size": len(bundle)}},
		"subject": map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": bundleTestSubject, "size": 2382},
	}
	if mutate != nil {
		mutate(manifest)
	}
	body := bundleTestJSON(t, manifest)
	digest := imageContentDigest(body)
	reg.put(bundleTestRepo+"/manifests/"+digest, "application/vnd.oci.image.manifest.v1+json", body)
	reg.put(bundleTestRepo+"/blobs/"+imageContentDigest(bundle), "application/octet-stream", bundle)
	return map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": digest, "size": len(body), "artifactType": SigstoreBundleMediaType}
}

func bundleTestIndex(t *testing.T, entries ...map[string]any) []byte {
	t.Helper()
	manifests := make([]any, 0, len(entries))
	for _, e := range entries {
		manifests = append(manifests, e)
	}
	return bundleTestJSON(t, map[string]any{"schemaVersion": 2, "mediaType": ociIndexMediaType, "manifests": manifests})
}

func pullTestBundles(client *RegistryClient) ([][]byte, error) {
	return client.PullSigstoreBundles(context.Background(), "registry.example/poyrazk/runner-node22@"+bundleTestSubject, bundleTestSubject, nil)
}

func TestSigstoreBundlesReadTheReferrersTagWhenTheAPIIsMissing(t *testing.T) {
	reg, client := newBundleTestRegistry(t)
	bundle := []byte(`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json"}`)
	entry := attachTestBundle(t, reg, bundle, nil)
	tag := bundleTestRepo + "/manifests/sha256-" + strings.Repeat("d", 64)
	reg.put(tag, ociIndexMediaType, bundleTestIndex(t, entry))

	got, err := pullTestBundles(client)
	if err != nil || len(got) != 1 || string(got[0]) != string(bundle) {
		t.Fatalf("bundles = %q, %v", got, err)
	}
	if !reg.requested(bundleTestRepo+"/referrers/"+bundleTestSubject) || !reg.requested(tag) {
		t.Fatal("the referrers API was not tried before the tag schema")
	}
}

func TestSigstoreBundlesPreferTheReferrersAPI(t *testing.T) {
	reg, client := newBundleTestRegistry(t)
	bundle := []byte(`{"from":"referrers api"}`)
	entry := attachTestBundle(t, reg, bundle, nil)
	reg.put(bundleTestRepo+"/referrers/"+bundleTestSubject, ociIndexMediaType, bundleTestIndex(t, entry))
	tag := bundleTestRepo + "/manifests/sha256-" + strings.Repeat("d", 64)

	got, err := pullTestBundles(client)
	if err != nil || len(got) != 1 || string(got[0]) != string(bundle) {
		t.Fatalf("bundles = %q, %v", got, err)
	}
	if reg.requested(tag) {
		t.Fatal("read the tag schema although the referrers API listed a bundle")
	}
}

func TestSigstoreBundlesFallBackWhenTheAPIListsNoBundle(t *testing.T) {
	reg, client := newBundleTestRegistry(t)
	bundle := []byte(`{"from":"tag"}`)
	entry := attachTestBundle(t, reg, bundle, nil)
	other := map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": "sha256:" + strings.Repeat("e", 64), "size": 10, "artifactType": "application/spdx+json"}
	reg.put(bundleTestRepo+"/referrers/"+bundleTestSubject, ociIndexMediaType, bundleTestIndex(t, other))
	reg.put(bundleTestRepo+"/manifests/sha256-"+strings.Repeat("d", 64), ociIndexMediaType, bundleTestIndex(t, entry))

	got, err := pullTestBundles(client)
	if err != nil || len(got) != 1 || string(got[0]) != string(bundle) {
		t.Fatalf("bundles = %q, %v", got, err)
	}
}

func TestSigstoreBundlesMissing(t *testing.T) {
	_, client := newBundleTestRegistry(t)
	if _, err := pullTestBundles(client); !errors.Is(err, ErrSigstoreBundleMissing) {
		t.Fatalf("err = %v, want ErrSigstoreBundleMissing", err)
	}
}

func TestSigstoreBundlesReadOnlyTheNewestBounded(t *testing.T) {
	reg, client := newBundleTestRegistry(t)
	var entries []map[string]any
	for i := 0; i < api.BaseProvenanceMaxBundles+3; i++ {
		entries = append(entries, attachTestBundle(t, reg, []byte(fmt.Sprintf(`{"n":%d}`, i)), nil))
	}
	reg.put(bundleTestRepo+"/manifests/sha256-"+strings.Repeat("d", 64), ociIndexMediaType, bundleTestIndex(t, entries...))

	got, err := pullTestBundles(client)
	if err != nil || len(got) != api.BaseProvenanceMaxBundles {
		t.Fatalf("read %d bundles, %v; want %d", len(got), err, api.BaseProvenanceMaxBundles)
	}
	if newest := fmt.Sprintf(`{"n":%d}`, api.BaseProvenanceMaxBundles+2); string(got[0]) != newest {
		t.Fatalf("first bundle = %s, want the newest %s", got[0], newest)
	}
}

func TestSigstoreBundlesRejectInvalidTransportContent(t *testing.T) {
	bundle := []byte(`{"bundle":true}`)
	for _, tc := range []struct {
		name   string
		mutate func(manifest map[string]any)
		entry  func(entry map[string]any)
		blob   []byte
		index  string
	}{
		{name: "another subject", mutate: func(m map[string]any) {
			m["subject"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("f", 64)
		}},
		{name: "no subject", mutate: func(m map[string]any) { delete(m, "subject") }},
		{name: "two bundles", mutate: func(m map[string]any) { m["layers"] = append(m["layers"].([]any), m["layers"].([]any)[0]) }},
		{name: "wrong layer type", mutate: func(m map[string]any) {
			m["layers"].([]any)[0].(map[string]any)["mediaType"] = "application/octet-stream"
		}},
		{name: "bundle over limit", mutate: func(m map[string]any) {
			m["layers"].([]any)[0].(map[string]any)["size"] = api.BaseProvenanceMaxBundleBytes + 1
		}},
		{name: "bundle digest mismatch", blob: []byte(`{"bundle":false}`)},
		{name: "manifest digest mismatch", entry: func(e map[string]any) { e["digest"] = "sha256:" + strings.Repeat("0", 64) }},
		{name: "manifest descriptor over limit", entry: func(e map[string]any) { e["size"] = api.BaseProvenanceMaxManifestBytes + 1 }},
		{name: "index content type", index: "application/vnd.oci.image.manifest.v1+json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, client := newBundleTestRegistry(t)
			entry := attachTestBundle(t, reg, bundle, tc.mutate)
			if tc.blob != nil {
				reg.put(bundleTestRepo+"/blobs/"+imageContentDigest(bundle), "application/octet-stream", tc.blob)
			}
			if tc.entry != nil {
				body := reg.objects[bundleTestRepo+"/manifests/"+entry["digest"].(string)]
				tc.entry(entry)
				reg.put(bundleTestRepo+"/manifests/"+entry["digest"].(string), body.contentType, body.body)
			}
			ct := ociIndexMediaType
			if tc.index != "" {
				ct = tc.index
			}
			reg.put(bundleTestRepo+"/manifests/sha256-"+strings.Repeat("d", 64), ct, bundleTestIndex(t, entry))
			got, err := pullTestBundles(client)
			if !errors.Is(err, ErrImageManifestInvalid) {
				t.Fatalf("bundles = %q, err = %v; want ErrImageManifestInvalid", got, err)
			}
		})
	}
}
