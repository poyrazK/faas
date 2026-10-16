package oci

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/imagechain"
)

// ImageResolution records both the original immutable object (possibly an
// index) and the selected image. SourceReference is the signature subject;
// Reference is the platform image used for config and layer reads.
type ImageResolution struct {
	InputReference  string
	SourceReference string
	SourceDigest    string
	Reference       string
	Digest          string
	Config          ImageConfig
	// Retained config may contain image environment values. Only private
	// producer records serialize this evidence; preflight JSON never does.
	Evidence *imagechain.Evidence `json:"-"`
}

// ImageInspection preserves the preflight API while sharing deployment resolution.
type ImageInspection = ImageResolution

// ImageResolver is additive so offline pullers retain their existing behavior.
type ImageResolver interface {
	ResolveImage(context.Context, string, *BasicAuth) (ImageResolution, error)
}

// InspectImage never downloads filesystem layers or executes a container.
func (c *RegistryClient) InspectImage(ctx context.Context, ref string, auth *BasicAuth) (ImageInspection, error) {
	return c.ResolveImage(ctx, ref, auth)
}

// ResolveImage selects a compatible child and verifies the content-addressed
// chain. The tag is read once, so source and selected digests cannot race.
func (c *RegistryClient) ResolveImage(ctx context.Context, ref string, auth *BasicAuth) (result ImageResolution, err error) {
	defer func() { err = scrubAuthFromError(err, auth) }()
	r, err := ParseReference(ref)
	if err != nil {
		return result, err
	}
	resolved, err := c.resolveImageManifest(ctx, r, auth)
	if err != nil {
		return result, err
	}
	cfg := resolved.config
	if cfg == nil {
		parsed, body, err := c.verifiedImageConfigBytes(ctx, r, resolved.manifest, auth)
		if err != nil {
			return result, err
		}
		cfg = &parsed
		resolved.configBody = body
	}
	if !cfg.SupportsProductionPlatform() {
		return result, &PlatformSelectionError{Reason: "image config is incompatible with the production fleet", Available: []string{cfg.OS + "/" + cfg.Architecture + "/" + cfg.Variant}}
	}
	r.Tag, r.Digest = "", resolved.sourceDigest
	sourceRef := r.String()
	r.Digest = resolved.digest
	evidence := &imagechain.Evidence{SourceManifest: resolved.sourceBody, Config: resolved.configBody}
	if resolved.sourceDigest != resolved.digest {
		evidence.SelectedManifest = resolved.body
	}
	if _, err := imagechain.Validate(evidence, resolved.sourceDigest, resolved.digest); err != nil {
		return result, fmt.Errorf("%w: retained image chain: %w", ErrImageManifestInvalid, err)
	}
	return ImageResolution{InputReference: ref, SourceReference: sourceRef, SourceDigest: resolved.sourceDigest, Reference: r.String(), Digest: resolved.digest, Config: *cfg, Evidence: evidence.Clone()}, nil
}
