package oci

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// ImageInspection describes one immutable image. InspectImage never fetches
// filesystem layers, executes the image, or writes to a registry.
type ImageInspection struct {
	Reference string
	Digest    string
	Config    ImageConfig
}

// InspectImage uses the deployment client's single-platform manifest policy.
// Hashing the fetched manifest avoids a tag-resolution race; checking both
// descriptors ensures the report describes the exact content it identifies.
// Credentials are used only for this invocation and are scrubbed from errors.
func (c *RegistryClient) InspectImage(ctx context.Context, ref string, auth *BasicAuth) (result ImageInspection, err error) {
	defer func() { err = scrubAuthFromError(err, auth) }()
	r, err := ParseReference(ref)
	if err != nil {
		return result, err
	}
	m, body, err := c.fetchManifestWithAuth(ctx, r, auth)
	if err != nil {
		return result, err
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	if r.Digest != "" && r.Digest != digest {
		return result, fmt.Errorf("%w: manifest content does not match requested digest", ErrImageManifestInvalid)
	}
	if m.SchemaVersion != 2 || len(m.Layers) == 0 {
		return result, fmt.Errorf("%w: expected schemaVersion 2 and at least one filesystem layer", ErrImageManifestInvalid)
	}
	if err := validateDigest(m.Config.Digest); err != nil {
		return result, fmt.Errorf("%w: invalid config descriptor", ErrImageManifestInvalid)
	}
	for _, layer := range m.Layers {
		if err := validateDigest(layer.Digest); err != nil {
			return result, fmt.Errorf("%w: invalid layer descriptor", ErrImageManifestInvalid)
		}
	}
	config, err := c.fetchBlobWithAuth(ctx, r, m.Config.Digest, auth)
	if err != nil {
		return result, err
	}
	if fmt.Sprintf("sha256:%x", sha256.Sum256(config)) != m.Config.Digest {
		return result, fmt.Errorf("%w: config content does not match its digest", ErrImageManifestInvalid)
	}
	cfg, err := parseImageConfig(config)
	if err != nil {
		return result, fmt.Errorf("%w: invalid image config: %w", ErrImageManifestInvalid, err)
	}
	r.Tag, r.Digest = "", digest
	return ImageInspection{Reference: r.String(), Digest: digest, Config: cfg}, nil
}
