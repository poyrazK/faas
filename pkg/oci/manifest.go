package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Descriptor mirrors the OCI content descriptor (spec §4.6, §9). The MediaType
// is what lets callers distinguish a layer blob from a config blob from an
// index — the registry's /v2/<repo>/blobs/<digest> endpoint serves all three
// and the consumer has to know which is which.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Manifest is the OCI image manifest (or its docker-distribution equivalent)
// after the registry has resolved it. The two media types we accept produce
// the same Go shape — a list of layers plus one config descriptor.
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

// PullManifest fetches the manifest for ref and returns its decoded contents.
// The caller is responsible for translating this into the rootfs build path
// (LayersAboveBase + rootfs.Builder).
//
// This is the M6 wire-up: previously only PullDigest existed, which is enough
// to resolve a digest but not enough to actually pull the layers. PullManifest
// is what imaged.handleDeployment calls after PullDigest to start building.
//
// PullManifest is the anonymous form (issue #461 / ADR-062); for
// per-app private-registry Basic Auth use PullManifestWithAuth. The
// legacy method delegates to PullManifestWithAuth(ctx, ref, nil) so
// the two paths can't drift.
func (c *RegistryClient) PullManifest(ctx context.Context, ref string) (Manifest, error) {
	return c.PullManifestWithAuth(ctx, ref, nil)
}

// PullManifestWithAuth is the AuthManifestPuller variant of PullManifest.
// `auth == nil` keeps the anonymous behaviour; pass a *BasicAuth to
// authenticate the bearer-token realm request (issue #461 / ADR-062).
// imaged threads the customer's per-app credential only for the app
// manifest + app blobs; base manifest + base blobs stay anonymous.
func (c *RegistryClient) PullManifestWithAuth(ctx context.Context, ref string, auth *BasicAuth) (Manifest, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return Manifest{}, err
	}
	resolved, err := c.resolveImageManifest(ctx, r, auth)
	if err != nil {
		return Manifest{}, err
	}
	var doc Manifest
	if err := json.Unmarshal(resolved.body, &doc); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode image manifest: %w", ErrImageManifestInvalid, err)
	}
	if doc.Config.Digest == "" {
		return Manifest{}, fmt.Errorf("%w: %s missing config descriptor",
			ErrImageManifestInvalid, r.String())
	}
	if len(doc.Layers) == 0 {
		return Manifest{}, fmt.Errorf("%w: %s has no layers",
			ErrImageManifestInvalid, r.String())
	}
	if err := validateDigest(doc.Config.Digest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s config: %s",
			ErrImageManifestInvalid, r.String(), err.Error())
	}
	for i, l := range doc.Layers {
		if err := validateDigest(l.Digest); err != nil {
			return Manifest{}, fmt.Errorf("%w: %s layer %d: %s",
				ErrImageManifestInvalid, r.String(), i, err.Error())
		}
	}
	return doc, nil
}

// PullBlob streams the bytes of a blob (layer or config) referenced by
// digest from repo. The caller MUST close the returned reader. The reader is
// NOT decompressed — layers are still gzipped tarballs, callers feed them to
// rootfs.ApplyLayerGz which handles the gunzip. As the reader reaches EOF,
// it verifies the streamed bytes against digest and returns an
// ErrImageManifestInvalid-wrapped error if they differ.
//
// PullBlob is the anonymous form (issue #461 / ADR-062); for
// per-app private-registry Basic Auth use PullBlobWithAuth. The
// legacy method delegates to PullBlobWithAuth(ctx, repo, digest, nil)
// so the two paths can't drift.
func (c *RegistryClient) PullBlob(ctx context.Context, repo, digest string) (io.ReadCloser, error) {
	return c.PullBlobWithAuth(ctx, repo, digest, nil)
}

// PullBlobWithAuth is the AuthManifestPuller variant of PullBlob.
// `auth == nil` keeps the anonymous behaviour; pass a *BasicAuth to
// authenticate the bearer-token realm request (issue #461 / ADR-062).
// imaged threads the customer's per-app credential only for the app
// manifest + app blobs; base manifest + base blobs stay anonymous.
func (c *RegistryClient) PullBlobWithAuth(ctx context.Context, repo, digest string, auth *BasicAuth) (io.ReadCloser, error) {
	if err := validateDigest(digest); err != nil {
		return nil, err
	}
	if repo == "" {
		return nil, fmt.Errorf("oci: empty repository")
	}
	// Parse the repository or full image reference for host/repository routing.
	// A pinned image reference must not acquire a second @digest suffix.
	r, err := ParseReference(repo)
	if err != nil {
		return nil, err
	}
	_, body, err := c.openBlobWithAuth(ctx, r, digest, auth)
	if err != nil {
		return nil, err
	}
	return body, nil
}
