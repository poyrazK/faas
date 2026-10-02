// Package imagechain validates retained OCI metadata without registry, state,
// storage or daemon dependencies. // adr: 431
package imagechain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociref"
)

var ErrInvalid = errors.New("imagechain: invalid image evidence")

type Evidence struct {
	SourceManifest []byte `json:"source_manifest"`
	// Empty for a direct manifest; its source already contains these bytes.
	SelectedManifest []byte `json:"selected_manifest,omitempty"`
	Config           []byte `json:"config"`
}

func (e *Evidence) Clone() *Evidence {
	if e == nil {
		return nil
	}
	return &Evidence{SourceManifest: bytes.Clone(e.SourceManifest), SelectedManifest: bytes.Clone(e.SelectedManifest), Config: bytes.Clone(e.Config)}
}
func Digest(body []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(body)) }

type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}
type Image struct {
	Config  Descriptor
	Layers  []Descriptor
	DiffIDs []string
}
type platform struct {
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Variant      string   `json:"variant"`
	OSVersion    string   `json:"os.version"`
	OSFeatures   []string `json:"os.features"`
	Features     []string `json:"features"`
}

func (p platform) compatible() bool {
	return p.OS == "linux" && p.Architecture == "amd64" && (p.Variant == "" || p.Variant == "v1") && p.OSVersion == "" && len(p.OSFeatures) == 0 && len(p.Features) == 0
}
func manifestMediaType(mt string) bool {
	return mt == "" || mt == "application/vnd.oci.image.manifest.v1+json" || mt == "application/vnd.docker.distribution.manifest.v2+json"
}

// Validate recomputes the signed source -> selected manifest -> config ->
// ordered layer descriptors/DiffIDs. It proves metadata, not consumed layers.
func Validate(e *Evidence, sourceDigest, selectedDigest string) (Image, error) {
	var result Image
	if e == nil || len(e.SourceManifest) == 0 || int64(len(e.SourceManifest)) > api.OCIManifestMaxBytes || int64(len(e.SelectedManifest)) > api.OCIManifestMaxBytes || len(e.Config) == 0 || int64(len(e.Config)) > api.OCIConfigMaxBytes ||
		ociref.ValidateDigest(sourceDigest) != nil || ociref.ValidateDigest(selectedDigest) != nil || Digest(e.SourceManifest) != sourceDigest {
		return result, ErrInvalid
	}
	selected := e.SourceManifest
	if sourceDigest != selectedDigest {
		selected = e.SelectedManifest
		if err := validateSelection(e.SourceManifest, selected, selectedDigest); err != nil {
			return result, err
		}
	} else if len(e.SelectedManifest) != 0 {
		return result, fmt.Errorf("%w: redundant direct manifest", ErrInvalid)
	}
	if Digest(selected) != selectedDigest {
		return result, fmt.Errorf("%w: selected manifest digest", ErrInvalid)
	}
	var manifest struct {
		SchemaVersion int          `json:"schemaVersion"`
		MediaType     string       `json:"mediaType"`
		Config        Descriptor   `json:"config"`
		Layers        []Descriptor `json:"layers"`
	}
	if json.Unmarshal(selected, &manifest) != nil || manifest.SchemaVersion != 2 || !manifestMediaType(manifest.MediaType) || len(manifest.Layers) == 0 || len(manifest.Layers) > api.OCIImageMaxLayers {
		return result, fmt.Errorf("%w: image manifest", ErrInvalid)
	}
	if manifest.Config.Size != int64(len(e.Config)) || ociref.ValidateDigest(manifest.Config.Digest) != nil || Digest(e.Config) != manifest.Config.Digest {
		return result, fmt.Errorf("%w: config descriptor", ErrInvalid)
	}
	var config struct {
		platform
		RootFS struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if json.Unmarshal(e.Config, &config) != nil || !config.compatible() || config.RootFS.Type != "layers" || len(config.RootFS.DiffIDs) != len(manifest.Layers) {
		return result, fmt.Errorf("%w: config platform/layer count", ErrInvalid)
	}
	for i, d := range manifest.Layers {
		if !ValidLayerDescriptor(d) || ociref.ValidateDigest(config.RootFS.DiffIDs[i]) != nil {
			return result, fmt.Errorf("%w: layer descriptor/DiffID", ErrInvalid)
		}
	}
	return Image{Config: manifest.Config, Layers: manifest.Layers, DiffIDs: config.RootFS.DiffIDs}, nil
}

func validateSelection(source, selected []byte, digest string) error {
	var index struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Manifests     []struct {
			Descriptor
			Platform *platform `json:"platform"`
		} `json:"manifests"`
	}
	if json.Unmarshal(source, &index) != nil || index.SchemaVersion != 2 || (index.MediaType != "application/vnd.oci.image.index.v1+json" && index.MediaType != "application/vnd.docker.distribution.manifest.list.v2+json") {
		return fmt.Errorf("%w: source index", ErrInvalid)
	}
	matches := map[string]Descriptor{}
	for _, d := range index.Manifests {
		if d.Platform == nil || !d.Platform.compatible() || d.MediaType == "" || !manifestMediaType(d.MediaType) {
			continue
		}
		if ociref.ValidateDigest(d.Digest) != nil || d.Size <= 0 {
			return fmt.Errorf("%w: selected descriptor", ErrInvalid)
		}
		if old, ok := matches[d.Digest]; ok && old != d.Descriptor {
			return fmt.Errorf("%w: conflicting descriptor", ErrInvalid)
		}
		matches[d.Digest] = d.Descriptor
	}
	d, ok := matches[digest]
	if len(matches) != 1 || !ok || d.Size != int64(len(selected)) || Digest(selected) != digest {
		return fmt.Errorf("%w: ambiguous/mismatched selected image", ErrInvalid)
	}
	return nil
}

func ValidLayerDescriptor(d Descriptor) bool {
	return ociref.ValidateDigest(d.Digest) == nil && d.Size > 0 && d.Size <= api.OCIImageMaxCompressedLayerBytes &&
		(d.MediaType == "" || d.MediaType == "application/vnd.oci.image.layer.v1.tar+gzip" || d.MediaType == "application/vnd.docker.image.rootfs.diff.tar.gzip" || d.MediaType == "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip" || d.MediaType == "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip")
}
