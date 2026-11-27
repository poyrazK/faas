package ociarchive

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
)

// ConvertResult describes the canonical archive Convert wrote.
type ConvertResult struct {
	ManifestDigest string
	// ImageName is the image's first repository tag, when the input names one.
	ImageName string
	Layers    int
}

// dockerSaveEntry is one image of a `docker save` manifest.json.
type dockerSaveEntry struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

type sourceLayer struct {
	path      string
	mediaType string // from an OCI manifest; empty for manifest.json inputs
}

type sourceImage struct {
	config []byte
	name   string
	layers []sourceLayer
}

// metadataMaxBytes bounds the JSON entries Convert keeps in memory.
const metadataMaxBytes = 16 << 20

// Convert reads a `docker save` (or `docker image save`, `podman save`) tar,
// in either the legacy Docker archive layout or the OCI layout Docker 25+
// writes, and writes the canonical single-image OCI archive Inspect accepts.
// want selects one image when the input holds several (a repository tag);
// empty means the input must hold exactly one. Uncompressed layers are gzip
// compressed and checked against the config's diff IDs. tmpDir holds one
// staged layer at a time.
func Convert(inputPath, want, tmpDir string, out io.Writer) (ConvertResult, error) {
	meta, err := readMetadata(inputPath)
	if err != nil {
		return ConvertResult{}, err
	}
	img, err := selectImage(meta, want)
	if err != nil {
		return ConvertResult{}, err
	}
	if err := CheckPlatform(img.config); err != nil {
		return ConvertResult{}, err
	}
	config, err := oci.ParseConfig(bytes.NewReader(img.config))
	if err != nil {
		return ConvertResult{}, invalid("parse image config: %v", err)
	}
	if len(config.DiffIDs) != len(img.layers) || len(img.layers) == 0 {
		return ConvertResult{}, invalid("image lists %d layers but its config lists %d diff IDs", len(img.layers), len(config.DiffIDs))
	}
	staged, err := stageLayers(inputPath, img.layers, config.DiffIDs, tmpDir)
	defer func() {
		for _, l := range staged {
			_ = os.Remove(l.path)
		}
	}()
	if err != nil {
		return ConvertResult{}, err
	}
	digest, err := writeCanonical(out, img.config, staged)
	if err != nil {
		return ConvertResult{}, err
	}
	return ConvertResult{ManifestDigest: digest, ImageName: img.name, Layers: len(staged)}, nil
}

// readMetadata keeps every small entry an image could reference as metadata:
// manifest.json, index.json and JSON-sized blobs. Layer bodies are skipped.
func readMetadata(inputPath string) (map[string][]byte, error) {
	f, err := os.Open(inputPath) //nolint:forbidigo // CLI-local archive the customer named.
	if err != nil {
		return nil, fmt.Errorf("open image archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	meta := map[string][]byte{}
	tr := tar.NewReader(bufio.NewReader(f))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return meta, nil
		}
		if err != nil {
			return nil, invalid("read image archive: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Size > metadataMaxBytes {
			continue
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if !strings.HasSuffix(name, ".json") && !strings.HasPrefix(name, "blobs/") {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, invalid("read %s: %v", name, err)
		}
		if strings.HasPrefix(name, "blobs/") && !json.Valid(b) {
			continue // a small layer, not metadata
		}
		meta[name] = b
	}
}

func selectImage(meta map[string][]byte, want string) (sourceImage, error) {
	if raw, ok := meta["manifest.json"]; ok {
		return selectDockerSaveImage(meta, raw, want)
	}
	if raw, ok := meta["index.json"]; ok {
		return selectOCIImage(meta, raw)
	}
	return sourceImage{}, invalid("not a docker save archive: manifest.json and index.json are both missing")
}

func selectDockerSaveImage(meta map[string][]byte, raw []byte, want string) (sourceImage, error) {
	var entries []dockerSaveEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return sourceImage{}, invalid("decode manifest.json: %v", err)
	}
	// want only disambiguates: `docker save <id>` records no tag at all.
	chosen := entries
	if len(entries) > 1 && want != "" {
		chosen = nil
		for _, e := range entries {
			if containsTag(e.RepoTags, want) {
				chosen = append(chosen, e)
			}
		}
		if len(chosen) == 0 {
			return sourceImage{}, invalid("the archive has no image tagged %q", want)
		}
	}
	if len(chosen) != 1 {
		return sourceImage{}, invalid("the archive holds %d images; save exactly one", len(chosen))
	}
	e := chosen[0]
	config, ok := meta[path.Clean(e.Config)]
	if !ok {
		return sourceImage{}, invalid("image config %q is missing", e.Config)
	}
	img := sourceImage{config: config}
	if len(e.RepoTags) > 0 {
		img.name = e.RepoTags[0]
	}
	for _, l := range e.Layers {
		img.layers = append(img.layers, sourceLayer{path: path.Clean(l)})
	}
	return img, nil
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want || t == "docker.io/library/"+want || t == "docker.io/"+want {
			return true
		}
	}
	return false
}

// selectOCIImage follows index.json, through one nested image index when
// present, to the linux/amd64 image manifest.
func selectOCIImage(meta map[string][]byte, raw []byte) (sourceImage, error) {
	var idx index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return sourceImage{}, invalid("decode index.json: %v", err)
	}
	if len(idx.Manifests) != 1 {
		return sourceImage{}, invalid("index.json names %d images; save exactly one", len(idx.Manifests))
	}
	desc := idx.Manifests[0]
	for depth := 0; desc.MediaType == MediaTypeOCIIndex || desc.MediaType == MediaTypeDockerList; depth++ {
		if depth > 0 {
			return sourceImage{}, invalid("nested image indexes are not supported")
		}
		b, ok := meta[blobPath(desc.Digest)]
		if !ok {
			return sourceImage{}, invalid("image index %s is missing", desc.Digest)
		}
		next, err := amd64Manifest(meta, b)
		if err != nil {
			return sourceImage{}, err
		}
		desc = next
	}
	b, ok := meta[blobPath(desc.Digest)]
	if !ok {
		return sourceImage{}, invalid("image manifest %s is missing", desc.Digest)
	}
	var m oci.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return sourceImage{}, invalid("decode image manifest: %v", err)
	}
	config, ok := meta[blobPath(m.Config.Digest)]
	if !ok {
		return sourceImage{}, invalid("image config %s is missing", m.Config.Digest)
	}
	img := sourceImage{config: config}
	for _, l := range m.Layers {
		img.layers = append(img.layers, sourceLayer{path: blobPath(l.Digest), mediaType: l.MediaType})
	}
	return img, nil
}

func amd64Manifest(meta map[string][]byte, raw []byte) (oci.Descriptor, error) {
	var list struct {
		Manifests []struct {
			oci.Descriptor
			Platform *platformConfig `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return oci.Descriptor{}, invalid("decode image index: %v", err)
	}
	for _, m := range list.Manifests {
		p := m.Platform
		if p == nil || p.OS != PlatformOS || p.Architecture != PlatformArch || (p.Variant != "" && p.Variant != "v1") {
			continue
		}
		if _, ok := meta[blobPath(m.Digest)]; ok {
			return m.Descriptor, nil
		}
	}
	return oci.Descriptor{}, invalid("the archive has no linux/amd64 image; rebuild with `docker build --platform linux/amd64` or pull with `docker pull --platform linux/amd64`")
}

func blobPath(digest string) string {
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
}

type stagedLayer struct {
	path      string
	digest    string
	size      int64
	mediaType string
}

// stageLayers makes one pass over the input, writing each referenced layer to
// tmpDir as a compressed blob in manifest order.
func stageLayers(inputPath string, layers []sourceLayer, diffIDs []string, tmpDir string) ([]stagedLayer, error) {
	want := map[string][]int{}
	for i, l := range layers {
		want[l.path] = append(want[l.path], i)
	}
	staged := make([]stagedLayer, len(layers))
	f, err := os.Open(inputPath) //nolint:forbidigo // CLI-local archive the customer named.
	if err != nil {
		return nil, fmt.Errorf("open image archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	tr := tar.NewReader(bufio.NewReader(f))
	var done []stagedLayer
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return done, invalid("read image archive: %v", err)
		}
		idxs, ok := want[path.Clean(strings.TrimPrefix(hdr.Name, "./"))]
		if !ok || hdr.Typeflag != tar.TypeReg {
			continue
		}
		layer, err := stageLayer(tr, layers[idxs[0]].mediaType, diffIDs[idxs[0]], tmpDir)
		if err != nil {
			return done, err
		}
		done = append(done, layer)
		for _, i := range idxs {
			if staged[i].path != "" {
				return done, invalid("layer %s appears twice", hdr.Name)
			}
			staged[i] = layer
		}
	}
	for i, l := range staged {
		if l.path == "" {
			return done, invalid("layer %d (%s) is missing from the archive", i, layers[i].path)
		}
	}
	return staged, nil
}

func stageLayer(r io.Reader, mediaType, diffID, tmpDir string) (stagedLayer, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	magic, _ := br.Peek(4)
	tmp, err := os.CreateTemp(tmpDir, "gregale-layer-*.blob")
	if err != nil {
		return stagedLayer{}, fmt.Errorf("stage layer: %w", err)
	}
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmp.Name())
		}
	}()
	digest := sha256.New()
	counter := &countWriter{w: io.MultiWriter(tmp, digest)}
	out := stagedLayer{path: tmp.Name()}
	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		out.mediaType = MediaTypeLayerGzip
		_, err = io.Copy(counter, br)
	case bytes.HasPrefix(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		out.mediaType = MediaTypeLayerZstd
		_, err = io.Copy(counter, br)
	default:
		if mediaType != "" && strings.Contains(mediaType, "+") {
			return stagedLayer{}, invalid("layer media type %q does not match its uncompressed content", mediaType)
		}
		out.mediaType = MediaTypeLayerGzip
		err = gzipLayer(counter, br, diffID)
	}
	if err != nil {
		return stagedLayer{}, err
	}
	out.digest = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	out.size = counter.n
	keep = true
	return out, nil
}

// gzipLayer compresses a raw layer deterministically (no name, zero mtime)
// so the same image converts to the same digests on every run, which keeps a
// resumed upload's identity stable.
func gzipLayer(w io.Writer, r io.Reader, diffID string) error {
	zw, err := gzip.NewWriterLevel(w, gzip.DefaultCompression)
	if err != nil {
		return err
	}
	raw := sha256.New()
	if _, err := io.Copy(zw, io.TeeReader(r, raw)); err != nil {
		return fmt.Errorf("compress layer: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("compress layer: %w", err)
	}
	if got := "sha256:" + hex.EncodeToString(raw.Sum(nil)); got != diffID {
		return invalid("layer content %s does not match the config diff ID %s", got, diffID)
	}
	return nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// writeCanonical writes oci-layout, index.json and each blob once.
func writeCanonical(out io.Writer, config []byte, layers []stagedLayer) (string, error) {
	configDigest := sha256Digest(config)
	manifest := oci.Manifest{SchemaVersion: 2, MediaType: MediaTypeOCIManifest,
		Config: oci.Descriptor{MediaType: MediaTypeOCIConfig, Digest: configDigest, Size: int64(len(config))}}
	for _, l := range layers {
		manifest.Layers = append(manifest.Layers, oci.Descriptor{MediaType: l.mediaType, Digest: l.digest, Size: l.size})
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	manifestDigest := sha256Digest(manifestBytes)
	indexBytes, err := json.Marshal(index{SchemaVersion: 2, MediaType: MediaTypeOCIIndex, Manifests: []oci.Descriptor{
		{MediaType: MediaTypeOCIManifest, Digest: manifestDigest, Size: int64(len(manifestBytes))}}})
	if err != nil {
		return "", err
	}
	tw := tar.NewWriter(out)
	for _, e := range []struct {
		name string
		body []byte
	}{
		{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{"index.json", indexBytes},
		{blobPath(manifestDigest), manifestBytes},
		{blobPath(configDigest), config},
	} {
		if err := writeEntry(tw, e.name, int64(len(e.body)), bytes.NewReader(e.body)); err != nil {
			return "", err
		}
	}
	written := map[string]bool{configDigest: true, manifestDigest: true}
	for _, l := range layers {
		if written[l.digest] {
			continue
		}
		written[l.digest] = true
		f, err := os.Open(l.path) //nolint:forbidigo // staged by stageLayer above.
		if err != nil {
			return "", fmt.Errorf("open staged layer: %w", err)
		}
		err = writeEntry(tw, blobPath(l.digest), l.size, f)
		_ = f.Close()
		if err != nil {
			return "", err
		}
	}
	if err := tw.Close(); err != nil {
		return "", fmt.Errorf("finish image archive: %w", err)
	}
	return manifestDigest, nil
}

func writeEntry(tw *tar.Writer, name string, size int64, r io.Reader) error {
	hdr := &tar.Header{Name: name, Mode: 0o644, Size: size, Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if _, err := io.Copy(tw, r); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
