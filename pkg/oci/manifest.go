package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	url := c.baseURL(r) + "/v2/" + r.Repository + "/manifests/" + r.ManifestRef()
	resp, err := c.getManifest(ctx, url, "")
	if err != nil {
		return Manifest{}, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		ch := parseChallenge(resp.Header.Get("Www-Authenticate"))
		_ = resp.Body.Close()
		token, err := c.fetchToken(ctx, ch, auth)
		if err != nil {
			return Manifest{}, err
		}
		resp, err = c.getManifest(ctx, url, token)
		if err != nil {
			return Manifest{}, err
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := fmt.Errorf("oci: manifest %s: registry returned %d: %s",
			r.String(), resp.StatusCode, string(body))
		// ADR-021: puller-side 404s map to the canonical
		// ErrImageNotFound sentinel (surface as 422). 4xx / 5xx that
		// aren't 404 keep their plain-text shape — those are not the
		// three puller-side failure modes this ADR closes.
		if resp.StatusCode == http.StatusNotFound {
			return Manifest{}, fmt.Errorf("%w: %s", ErrImageNotFound, msg.Error())
		}
		return Manifest{}, msg
	}

	// ADR-140: when the response Content-Type is an OCI image-index /
	// Docker manifest-list, walk Manifests[] to the per-arch descriptor
	// that matches the host's PlatformMatcher (default
	// DefaultPlatformMatcherFromGOARCH). The walk is bounded to two
	// hops — a manifest list whose selected entry is itself a list
	// returns ErrImageManifestInvalid. digest-pinned references
	// resolve directly to a single-arch manifest and skip the walk.
	mt := resp.Header.Get("Content-Type")
	var bodyBytes []byte
	if isIndexContentType(mt) {
		var err error
		bodyBytes, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return Manifest{}, fmt.Errorf("%w: read index body %s: %s",
				ErrImageManifestInvalid, r.String(), err.Error())
		}
		bodyBytes, err = c.walkIndexToAuth(ctx, r, bodyBytes, auth, 1)
		if err != nil {
			return Manifest{}, err
		}
		var doc Manifest
		if err := json.Unmarshal(bodyBytes, &doc); err != nil {
			return Manifest{}, fmt.Errorf("%w: decode manifest %s: %s",
				ErrImageManifestInvalid, r.String(), err.Error())
		}
		return validateManifestShape(r.String(), doc)
	}

	var doc Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&doc); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode manifest %s: %s",
			ErrImageManifestInvalid, r.String(), err.Error())
	}
	return validateManifestShape(r.String(), doc)
}

// validateManifestShape enforces the post-decode structural checks
// (config descriptor present, layers non-empty, every digest parses)
// used by PullManifestWithAuth. Lifts failures to ErrImageManifestInvalid
// (ADR-021) so pkg/api.SentinelToCode maps them to RFC 7807
// CodeImageManifestInvalid (422).
func validateManifestShape(ref string, doc Manifest) (Manifest, error) {
	if doc.Config.Digest == "" {
		return Manifest{}, fmt.Errorf("%w: %s missing config descriptor",
			ErrImageManifestInvalid, ref)
	}
	if len(doc.Layers) == 0 {
		return Manifest{}, fmt.Errorf("%w: %s has no layers",
			ErrImageManifestInvalid, ref)
	}
	if err := validateDigest(doc.Config.Digest); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s config: %s",
			ErrImageManifestInvalid, ref, err.Error())
	}
	for i, l := range doc.Layers {
		if err := validateDigest(l.Digest); err != nil {
			return Manifest{}, fmt.Errorf("%w: %s layer %d: %s",
				ErrImageManifestInvalid, ref, i, err.Error())
		}
	}
	return doc, nil
}

// PullBlob streams the bytes of a blob (layer or config) referenced by
// digest from repo. The caller MUST close the returned reader. The reader is
// NOT decompressed — layers are still gzipped tarballs, callers feed them to
// rootfs.ApplyLayerGz which handles the gunzip.
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
	// Synthesize a partial reference just to get the baseURL/host wiring.
	r, err := ParseReference(repo + "@" + digest)
	if err != nil {
		return nil, err
	}
	_, body, err := c.openBlobWithAuth(ctx, r, digest, auth)
	if err != nil {
		return nil, err
	}
	return body, nil
}
