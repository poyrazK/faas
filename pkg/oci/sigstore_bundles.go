package oci

// ADR-20261029155852: the Sigstore bundles actions/attest-build-provenance
// pushes beside a platform image. This is transport only. Every manifest and
// bundle is content-addressed and bounded here; the caller verifies each
// bundle with cosign, so nothing a registry says establishes provenance.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// SigstoreBundleMediaType is the artifact and layer type of a Sigstore bundle
// attached to an image (Sigstore bundle v0.3).
const SigstoreBundleMediaType = "application/vnd.dev.sigstore.bundle.v0.3+json"

const ociIndexMediaType = "application/vnd.oci.image.index.v1+json"

var ErrSigstoreBundleMissing = errors.New("oci: sigstore bundle missing")

// SigstoreBundlePuller is additive to Puller: only the platform base
// provenance check reads bundles. Auth is scoped to the subject's repository.
type SigstoreBundlePuller interface {
	PullSigstoreBundles(context.Context, string, string, *BasicAuth) ([][]byte, error)
}

type sigstoreReferrersIndex struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Manifests     []struct {
		Descriptor
		ArtifactType string `json:"artifactType"`
	} `json:"manifests"`
}

type sigstoreBundleManifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Layers        []Descriptor `json:"layers"`
	Subject       *Descriptor  `json:"subject"`
}

// PullSigstoreBundles returns the Sigstore bundles attached to the manifest
// digest in ref's repository. It reads the OCI 1.1 referrers API and, when the
// registry has none or lists no bundle there, the referrers tag schema
// (sha256-<hex>), which is where GHCR keeps them. Each bundle's own manifest
// must name digest as its subject. At most api.BaseProvenanceMaxBundles
// bundles are read, the most recently listed first.
func (c *RegistryClient) PullSigstoreBundles(ctx context.Context, ref, digest string, auth *BasicAuth) (bundles [][]byte, err error) {
	defer func() { err = scrubAuthFromError(err, auth) }()
	r, err := ParseReference(ref)
	if err != nil {
		return nil, err
	}
	if err := validateDigest(digest); err != nil {
		return nil, fmt.Errorf("%w: invalid bundle subject digest", ErrImageManifestInvalid)
	}
	access, err := c.registryAuth(ctx, r, auth)
	if err != nil {
		return nil, err
	}
	candidates, err := c.sigstoreBundleCandidates(ctx, r, digest, access)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrSigstoreBundleMissing
	}
	for _, candidate := range candidates {
		bundle, err := c.readSigstoreBundle(ctx, r, candidate, digest, access)
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, bundle)
	}
	return bundles, nil
}

func (c *RegistryClient) sigstoreBundleCandidates(ctx context.Context, r Reference, digest string, auth *BasicAuth) ([]Descriptor, error) {
	base := c.baseURL(r) + "/v2/" + r.Repository
	urls := []string{
		base + "/referrers/" + digest + "?artifactType=" + SigstoreBundleMediaType,
		base + "/manifests/sha256-" + strings.TrimPrefix(digest, "sha256:"),
	}
	for _, url := range urls {
		body, ct, err := c.fetchManifestJSONWithAuthLimit(ctx, url, auth, api.BaseProvenanceMaxManifestBytes)
		if errors.Is(err, ErrImageNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		candidates, err := parseSigstoreReferrers(body, ct)
		if err != nil {
			return nil, err
		}
		if len(candidates) > 0 {
			return candidates, nil
		}
	}
	return nil, nil
}

func parseSigstoreReferrers(body []byte, contentType string) ([]Descriptor, error) {
	var index sigstoreReferrersIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return nil, fmt.Errorf("%w: invalid referrers index JSON", ErrImageManifestInvalid)
	}
	mt, _, ctErr := mime.ParseMediaType(contentType)
	if index.SchemaVersion != 2 || ctErr != nil || mt != ociIndexMediaType || (index.MediaType != "" && index.MediaType != mt) {
		return nil, fmt.Errorf("%w: unsupported referrers index format", ErrImageManifestInvalid)
	}
	var candidates []Descriptor
	for _, m := range index.Manifests {
		if m.ArtifactType != SigstoreBundleMediaType {
			continue
		}
		if validateDigest(m.Digest) != nil || m.Size <= 0 || m.Size > api.BaseProvenanceMaxManifestBytes {
			return nil, fmt.Errorf("%w: invalid bundle manifest descriptor", ErrImageManifestInvalid)
		}
		candidates = append(candidates, m.Descriptor)
	}
	// The tag schema appends each new referrer, so the newest are last.
	if len(candidates) > api.BaseProvenanceMaxBundles {
		candidates = candidates[len(candidates)-api.BaseProvenanceMaxBundles:]
	}
	for i, j := 0, len(candidates)-1; i < j; i, j = i+1, j-1 {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	}
	return candidates, nil
}

func (c *RegistryClient) readSigstoreBundle(ctx context.Context, r Reference, desc Descriptor, subject string, auth *BasicAuth) ([]byte, error) {
	body, ct, err := c.fetchManifestJSONWithAuthLimit(ctx, c.baseURL(r)+"/v2/"+r.Repository+"/manifests/"+desc.Digest, auth, api.BaseProvenanceMaxManifestBytes)
	if err != nil {
		return nil, err
	}
	if imageContentDigest(body) != desc.Digest {
		return nil, fmt.Errorf("%w: bundle manifest does not match its descriptor", ErrImageManifestInvalid)
	}
	var manifest sigstoreBundleManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("%w: invalid bundle manifest JSON", ErrImageManifestInvalid)
	}
	mt, _, ctErr := mime.ParseMediaType(ct)
	if manifest.SchemaVersion != 2 || ctErr != nil || !isImageManifest(mt, "") || (manifest.MediaType != "" && manifest.MediaType != mt) {
		return nil, fmt.Errorf("%w: unsupported bundle manifest format", ErrImageManifestInvalid)
	}
	if manifest.Subject == nil || manifest.Subject.Digest != subject {
		return nil, fmt.Errorf("%w: bundle manifest names another subject", ErrImageManifestInvalid)
	}
	if len(manifest.Layers) != 1 {
		return nil, fmt.Errorf("%w: bundle manifest must carry exactly one bundle", ErrImageManifestInvalid)
	}
	layer := manifest.Layers[0]
	if layer.MediaType != SigstoreBundleMediaType || validateDigest(layer.Digest) != nil || layer.Size <= 0 || layer.Size > api.BaseProvenanceMaxBundleBytes {
		return nil, fmt.Errorf("%w: unsupported bundle descriptor", ErrImageManifestInvalid)
	}
	_, rc, err := c.openBlobWithAuth(ctx, r, layer.Digest, auth)
	if err != nil {
		return nil, err
	}
	bundle, readErr := io.ReadAll(io.LimitReader(rc, api.BaseProvenanceMaxBundleBytes+1))
	if err := errors.Join(readErr, rc.Close()); err != nil {
		return nil, fmt.Errorf("oci: read sigstore bundle: %w", err)
	}
	if int64(len(bundle)) != layer.Size || imageContentDigest(bundle) != layer.Digest {
		return nil, fmt.Errorf("%w: sigstore bundle does not match its descriptor", ErrImageManifestInvalid)
	}
	return bundle, nil
}

var _ SigstoreBundlePuller = (*RegistryClient)(nil)
