// Package ociarchive reads and writes the single-image OCI layout archive a
// customer uploads to deploy an image that exists only on their machine
// (ADR-20261127120000). The CLI converts `docker save` output into the
// canonical shape; apid and builderd inspect it before it reaches imaged,
// whose local OCI loader remains the authoritative layer verifier.
package ociarchive

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// Media types of the canonical archive and the inputs it accepts.
const (
	MediaTypeOCIIndex       = "application/vnd.oci.image.index.v1+json"
	MediaTypeOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeOCIConfig      = "application/vnd.oci.image.config.v1+json"
	MediaTypeDockerConfig   = "application/vnd.docker.container.image.v1+json"
	MediaTypeLayerGzip      = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeLayerZstd      = "application/vnd.oci.image.layer.v1.tar+zstd"
	MediaTypeDockerGzip     = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// Platform is the only target Gregale executes (ADR-146).
const (
	PlatformOS   = "linux"
	PlatformArch = "amd64"
)

// ErrInvalid wraps every refusal so callers can map it to a 4xx.
var ErrInvalid = errors.New("invalid image archive")

var blobName = regexp.MustCompile(`^blobs/sha256/([0-9a-f]{64})$`)

// Summary is what apid records about an accepted archive.
type Summary struct {
	ManifestDigest string
	ConfigDigest   string
	Layers         int
	LayerBytes     int64
	// ExposedPort is the image's single declared TCP port, or 0.
	ExposedPort int
	// LayerDescriptors are the manifest's layers, in order.
	LayerDescriptors []oci.Descriptor
}

type index struct {
	SchemaVersion int              `json:"schemaVersion"`
	MediaType     string           `json:"mediaType"`
	Manifests     []oci.Descriptor `json:"manifests"`
}

type platformConfig struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

type blobInfo struct {
	size  int64
	small []byte // kept only for metadata-sized blobs
}

// Inspect verifies a canonical archive in one streaming pass: only the
// oci-layout, index.json and content-addressed blobs entries are allowed,
// every blob's bytes must match its name, the index names exactly one
// linux/amd64 image manifest, and every layer is gzip or zstd compressed.
// Layer diff IDs are left to imaged, which decompresses each layer anyway.
func Inspect(r io.Reader) (Summary, error) {
	tr := tar.NewReader(r)
	var indexBytes []byte
	sawLayout := false
	blobs := map[string]blobInfo{}
	var kept int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Summary{}, invalid("read tar: %v", err)
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if hdr.Typeflag == tar.TypeDir {
			if name == "blobs/" || name == "blobs/sha256/" || name == "blobs" || name == "blobs/sha256" {
				continue
			}
			return Summary{}, invalid("unexpected directory %q", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			return Summary{}, invalid("entry %q is not a regular file", hdr.Name)
		}
		switch name {
		case "oci-layout":
			if sawLayout {
				return Summary{}, invalid("duplicate oci-layout")
			}
			sawLayout = true
			if _, err := readBounded(tr, 4<<10); err != nil {
				return Summary{}, invalid("oci-layout: %v", err)
			}
			continue
		case "index.json":
			if indexBytes != nil {
				return Summary{}, invalid("duplicate index.json")
			}
			if indexBytes, err = readBounded(tr, api.LocalOCIMaxIndexBytes); err != nil {
				return Summary{}, invalid("index.json: %v", err)
			}
			continue
		}
		m := blobName.FindStringSubmatch(name)
		if m == nil {
			return Summary{}, invalid("unexpected entry %q", hdr.Name)
		}
		digest := "sha256:" + m[1]
		if _, dup := blobs[digest]; dup {
			return Summary{}, invalid("duplicate blob %s", digest)
		}
		info, err := hashBlob(tr, m[1], hdr.Size)
		if err != nil {
			return Summary{}, err
		}
		if kept += int64(len(info.small)); kept > maxKeptMetadataBytes {
			return Summary{}, invalid("the archive holds more than %d MiB of JSON blobs", maxKeptMetadataBytes>>20)
		}
		blobs[digest] = info
	}
	if !sawLayout || indexBytes == nil {
		return Summary{}, invalid("not an OCI image layout: oci-layout and index.json are required")
	}
	return summarize(indexBytes, blobs)
}

// maxKeptMetadataBytes bounds the blob bytes Inspect holds in memory: a
// manifest and a config, never layers.
const maxKeptMetadataBytes = api.LocalOCIMaxManifestBytes + api.LocalOCIMaxConfigBytes

// hashBlob verifies a blob's digest. It keeps the bytes only of a blob that
// may be a manifest or config: small enough and starting with '{'. Layers are
// gzip or zstd, whose magic bytes never start that way, so they stream through.
func hashBlob(r io.Reader, wantHex string, size int64) (blobInfo, error) {
	h := sha256.New()
	var first [1]byte
	n0, err := io.ReadFull(r, first[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return blobInfo{}, invalid("read blob sha256:%s: %v", wantHex, err)
	}
	h.Write(first[:n0])
	keep := n0 == 1 && first[0] == '{' && size <= api.LocalOCIMaxConfigBytes
	var small strings.Builder
	w := io.Writer(h)
	if keep {
		small.Write(first[:])
		w = io.MultiWriter(h, &small)
	}
	n, err := io.Copy(w, r)
	n += int64(n0)
	if err != nil {
		return blobInfo{}, invalid("read blob sha256:%s: %v", wantHex, err)
	}
	if hex.EncodeToString(h.Sum(nil)) != wantHex {
		return blobInfo{}, invalid("blob sha256:%s content does not match its digest", wantHex)
	}
	info := blobInfo{size: n}
	if keep {
		info.small = []byte(small.String())
	}
	return info, nil
}

func summarize(indexBytes []byte, blobs map[string]blobInfo) (Summary, error) {
	var idx index
	if err := json.Unmarshal(indexBytes, &idx); err != nil {
		return Summary{}, invalid("decode index.json: %v", err)
	}
	if idx.SchemaVersion != 2 || (idx.MediaType != "" && idx.MediaType != MediaTypeOCIIndex) {
		return Summary{}, invalid("unsupported index.json format")
	}
	if len(idx.Manifests) != 1 {
		return Summary{}, invalid("index.json names %d manifests, want exactly one image", len(idx.Manifests))
	}
	desc := idx.Manifests[0]
	if desc.MediaType != MediaTypeOCIManifest && desc.MediaType != MediaTypeDockerManifest {
		return Summary{}, invalid("index.json must name an image manifest, got %q", desc.MediaType)
	}
	manifestBytes, err := smallBlob(blobs, desc, api.LocalOCIMaxManifestBytes)
	if err != nil {
		return Summary{}, err
	}
	var manifest oci.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Summary{}, invalid("decode manifest: %v", err)
	}
	if manifest.SchemaVersion != 2 || (manifest.MediaType != MediaTypeOCIManifest && manifest.MediaType != MediaTypeDockerManifest) {
		return Summary{}, invalid("unsupported image manifest format")
	}
	if manifest.Config.MediaType != MediaTypeOCIConfig && manifest.Config.MediaType != MediaTypeDockerConfig {
		return Summary{}, invalid("unsupported image config media type %q", manifest.Config.MediaType)
	}
	configBytes, err := smallBlob(blobs, manifest.Config, api.LocalOCIMaxConfigBytes)
	if err != nil {
		return Summary{}, err
	}
	if err := CheckPlatform(configBytes); err != nil {
		return Summary{}, err
	}
	config, err := oci.ParseConfig(strings.NewReader(string(configBytes)))
	if err != nil {
		return Summary{}, invalid("parse image config: %v", err)
	}
	if len(manifest.Layers) == 0 || len(manifest.Layers) > api.LocalOCIMaxLayers {
		return Summary{}, invalid("image has %d layers", len(manifest.Layers))
	}
	if len(config.DiffIDs) != len(manifest.Layers) {
		return Summary{}, invalid("manifest has %d layers but config lists %d diff IDs", len(manifest.Layers), len(config.DiffIDs))
	}
	var total int64
	for i, layer := range manifest.Layers {
		switch layer.MediaType {
		case MediaTypeLayerGzip, MediaTypeLayerZstd, MediaTypeDockerGzip:
		default:
			return Summary{}, invalid("layer %d media type %q is not gzip or zstd compressed", i, layer.MediaType)
		}
		info, ok := blobs[layer.Digest]
		if !ok || info.size != layer.Size {
			return Summary{}, invalid("layer %d blob %s is missing or has the wrong size", i, layer.Digest)
		}
		total += layer.Size
	}
	port, _ := oci.SingleTCPExposedPort(config.ExposedPorts)
	return Summary{ManifestDigest: desc.Digest, ConfigDigest: manifest.Config.Digest, Layers: len(manifest.Layers), LayerBytes: total, ExposedPort: port, LayerDescriptors: manifest.Layers}, nil
}

// CheckPlatform refuses an image config that is not linux/amd64.
func CheckPlatform(configBytes []byte) error {
	var p platformConfig
	if err := json.Unmarshal(configBytes, &p); err != nil {
		return invalid("decode image config: %v", err)
	}
	if p.OS != PlatformOS || p.Architecture != PlatformArch || (p.Variant != "" && p.Variant != "v1") {
		got := p.OS + "/" + p.Architecture
		if p.Variant != "" {
			got += "/" + p.Variant
		}
		return invalid("image platform is %s; Gregale runs linux/amd64 images. Rebuild with `docker build --platform linux/amd64` or pull with `docker pull --platform linux/amd64`", got)
	}
	return nil
}

func smallBlob(blobs map[string]blobInfo, desc oci.Descriptor, maxBytes int64) ([]byte, error) {
	info, ok := blobs[desc.Digest]
	if !ok {
		return nil, invalid("blob %s is missing", desc.Digest)
	}
	if info.size != desc.Size || info.size > maxBytes || info.small == nil {
		return nil, invalid("blob %s is not a JSON document of the declared size", desc.Digest)
	}
	return info.small, nil
}

func readBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("larger than %d bytes", maxBytes)
	}
	return b, nil
}
