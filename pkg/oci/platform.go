package oci

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"mime"
	"sort"
)

// Production images target the x86_64 fleet, independent of the CLI host.
const ImageOS = "linux"
const ImageArchitecture = "amd64"

// PlatformSelectionError carries only platform metadata, never registry bodies
// or credentials. It remains an ErrImageManifestInvalid for deployment errors.
type PlatformSelectionError struct {
	Reason    string
	Available []string
}

func (e *PlatformSelectionError) Error() string {
	return fmt.Sprintf("oci: cannot select linux/amd64 image: %s; available platforms: %q", e.Reason, e.Available)
}
func (e *PlatformSelectionError) Unwrap() error { return ErrImageManifestInvalid }

type imagePlatform struct {
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Variant      string   `json:"variant"`
	OSVersion    string   `json:"os.version"`
	OSFeatures   []string `json:"os.features"`
	Features     []string `json:"features"`
}

func (p imagePlatform) compatible() bool {
	return p.OS == ImageOS && p.Architecture == ImageArchitecture && (p.Variant == "" || p.Variant == "v1") && p.OSVersion == "" && len(p.OSFeatures) == 0 && len(p.Features) == 0
}

type imageIndex struct {
	SchemaVersion int `json:"schemaVersion"`
	Manifests     []struct {
		Descriptor
		Platform *imagePlatform `json:"platform"`
	} `json:"manifests"`
}

type resolvedImageManifest struct {
	manifest     imageManifest
	body         []byte
	sourceDigest string
	digest       string
	config       *ImageConfig // verified when selected through an index
}

func imageContentDigest(body []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(body)) }

func imageMediaType(contentType, mediaType string) string {
	if mediaType != "" {
		return mediaType
	}
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		return mt
	}
	return contentType
}

func isImageIndex(mt string) bool {
	return mt == "application/vnd.oci.image.index.v1+json" || mt == "application/vnd.docker.distribution.manifest.list.v2+json"
}

// resolveImageManifest is shared by config, layer, manifest and inspection
// readers. PullDigest intentionally retains the digest of the original object:
// cosign signatures may cover the index rather than the selected image.
func (c *RegistryClient) resolveImageManifest(ctx context.Context, r Reference, auth *BasicAuth) (resolvedImageManifest, error) {
	var result resolvedImageManifest
	body, ct, err := c.fetchManifestJSONWithAuth(ctx, c.baseURL(r)+"/v2/"+r.Repository+"/manifests/"+r.ManifestRef(), auth)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(body, &result.manifest); err != nil {
		return result, fmt.Errorf("%w: decode manifest: %w", ErrImageManifestInvalid, err)
	}
	result.body, result.sourceDigest, result.digest = body, imageContentDigest(body), imageContentDigest(body)
	if r.Digest != "" && r.Digest != result.sourceDigest {
		return result, fmt.Errorf("%w: manifest content does not match requested digest", ErrImageManifestInvalid)
	}
	mt := imageMediaType(ct, result.manifest.MediaType)
	if !isImageIndex(mt) {
		if !isImageManifest(mt, "") {
			return result, fmt.Errorf("%w: unsupported image media type %q", ErrImageManifestInvalid, mt)
		}
		return result, nil
	}
	child, err := selectImagePlatform(body)
	if err != nil {
		return result, err
	}
	childRef := r
	childRef.Tag, childRef.Digest = "", child.Digest
	childBody, childCT, err := c.fetchManifestJSONWithAuth(ctx, c.baseURL(childRef)+"/v2/"+r.Repository+"/manifests/"+child.Digest, auth)
	if err != nil {
		return result, err
	}
	if imageContentDigest(childBody) != child.Digest || int64(len(childBody)) != child.Size {
		return result, fmt.Errorf("%w: selected manifest does not match index descriptor", ErrImageManifestInvalid)
	}
	var m imageManifest
	if err := json.Unmarshal(childBody, &m); err != nil {
		return result, fmt.Errorf("%w: decode selected manifest: %w", ErrImageManifestInvalid, err)
	}
	if !isImageManifest(childCT, m.MediaType) || m.SchemaVersion != 2 {
		return result, fmt.Errorf("%w: selected descriptor is not an image manifest", ErrImageManifestInvalid)
	}
	cfg, err := c.verifiedImageConfig(ctx, r, m, auth)
	if err != nil {
		return result, err
	}
	if !cfg.SupportsProductionPlatform() {
		return result, &PlatformSelectionError{Reason: "selected config disagrees with index platform", Available: []string{cfg.OS + "/" + cfg.Architecture}}
	}
	result.manifest, result.body, result.digest, result.config = m, childBody, child.Digest, &cfg
	return result, nil
}

func selectImagePlatform(body []byte) (Descriptor, error) {
	var index imageIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return Descriptor{}, fmt.Errorf("%w: decode index: %w", ErrImageManifestInvalid, err)
	}
	if index.SchemaVersion != 2 {
		return Descriptor{}, fmt.Errorf("%w: index schemaVersion must be 2", ErrImageManifestInvalid)
	}
	available := []string{}
	matches := map[string]Descriptor{}
	var selected Descriptor
	for _, entry := range index.Manifests {
		if entry.Platform == nil {
			continue
		}
		p := entry.Platform
		label := p.OS + "/" + p.Architecture
		if p.Variant != "" {
			label += "/" + p.Variant
		}
		available = append(available, label)
		if !p.compatible() || !isImageManifest(entry.MediaType, "") {
			continue
		}
		if err := validateDigest(entry.Digest); err != nil || entry.Size <= 0 {
			return Descriptor{}, fmt.Errorf("%w: invalid linux/amd64 descriptor", ErrImageManifestInvalid)
		}
		if previous, exists := matches[entry.Digest]; exists && previous != entry.Descriptor {
			return Descriptor{}, fmt.Errorf("%w: conflicting descriptors for the same digest", ErrImageManifestInvalid)
		}
		matches[entry.Digest] = entry.Descriptor
		selected = entry.Descriptor
	}
	sort.Strings(available)
	if len(matches) == 0 {
		return Descriptor{}, &PlatformSelectionError{Reason: "no compatible image manifest (nested indexes and additional CPU/OS requirements are not supported)", Available: available}
	}
	if len(matches) > 1 {
		return Descriptor{}, &PlatformSelectionError{Reason: "multiple compatible image manifests; pin the intended child digest", Available: available}
	}
	return selected, nil
}

func (c *RegistryClient) verifiedImageConfig(ctx context.Context, r Reference, m imageManifest, auth *BasicAuth) (ImageConfig, error) {
	if m.SchemaVersion != 2 || len(m.Layers) == 0 {
		return ImageConfig{}, fmt.Errorf("%w: expected schemaVersion 2 and at least one filesystem layer", ErrImageManifestInvalid)
	}
	if err := validateDigest(m.Config.Digest); err != nil {
		return ImageConfig{}, fmt.Errorf("%w: invalid config descriptor", ErrImageManifestInvalid)
	}
	for _, layer := range m.Layers {
		if err := validateDigest(layer.Digest); err != nil {
			return ImageConfig{}, fmt.Errorf("%w: invalid layer descriptor", ErrImageManifestInvalid)
		}
	}
	config, err := c.fetchBlobWithAuth(ctx, r, m.Config.Digest, auth)
	if err != nil {
		return ImageConfig{}, err
	}
	if imageContentDigest(config) != m.Config.Digest {
		return ImageConfig{}, fmt.Errorf("%w: config content does not match its digest", ErrImageManifestInvalid)
	}
	cfg, err := parseImageConfig(config)
	if err != nil {
		return ImageConfig{}, fmt.Errorf("%w: invalid image config: %w", ErrImageManifestInvalid, err)
	}
	return cfg, nil
}

// SupportsProductionPlatform reports compatibility with the baseline x86 fleet.
func (c ImageConfig) SupportsProductionPlatform() bool {
	return (imagePlatform{OS: c.OS, Architecture: c.Architecture, Variant: c.Variant}).compatible()
}
