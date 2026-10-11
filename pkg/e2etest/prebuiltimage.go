package e2etest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// prebuiltImageDescriptor is the image.json written next to a prebuilt
// fixture: the config file name and the ordered gzip layer blobs.
type prebuiltImageDescriptor struct {
	Config string `json:"config"`
	Layers []struct {
		File   string `json:"file"`
		Digest string `json:"digest"`
		Size   int    `json:"size"`
	} `json:"layers"`
}

// PrebuiltImage loads an OCI image assembled outside the test (for example
// a real Node runtime plus the ADR-958 tracing bundle) from dir/image.json
// and serves it like the generated fixtures. Layer digests are verified.
func PrebuiltImage(repo, dir string) (fakeImage, string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "image.json"))
	if err != nil {
		return fakeImage{}, "", fmt.Errorf("prebuilt image: %w", err)
	}
	var desc prebuiltImageDescriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return fakeImage{}, "", fmt.Errorf("prebuilt image descriptor: %w", err)
	}
	configBytes, err := os.ReadFile(filepath.Join(dir, filepath.Base(desc.Config)))
	if err != nil {
		return fakeImage{}, "", fmt.Errorf("prebuilt image config: %w", err)
	}
	sum := sha256.Sum256(configBytes)
	img := fakeImage{configDigest: "sha256:" + hex.EncodeToString(sum[:]), configBytes: configBytes}
	layers := make([]map[string]any, 0, len(desc.Layers))
	for _, layer := range desc.Layers {
		blob, err := os.ReadFile(filepath.Join(dir, filepath.Base(layer.File)))
		if err != nil {
			return fakeImage{}, "", fmt.Errorf("prebuilt image layer: %w", err)
		}
		layerSum := sha256.Sum256(blob)
		if "sha256:"+hex.EncodeToString(layerSum[:]) != layer.Digest {
			return fakeImage{}, "", fmt.Errorf("prebuilt image layer %s: digest mismatch", layer.File)
		}
		img.layerBlobs = append(img.layerBlobs, blobEntry{digest: layer.Digest, bytes: blob})
		layers = append(layers, map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": layer.Digest, "size": len(blob)})
	}
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config":        map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": img.configDigest, "size": len(configBytes)},
		"layers":        layers,
	}
	img.manifestBytes, _ = json.Marshal(manifest)
	manifestSum := sha256.Sum256(img.manifestBytes)
	img.manifestDigest = "sha256:" + hex.EncodeToString(manifestSum[:])
	img.manifestMT = "application/vnd.oci.image.manifest.v1+json"
	return img, fmt.Sprintf("%s@%s", repo, img.manifestDigest), nil
}
