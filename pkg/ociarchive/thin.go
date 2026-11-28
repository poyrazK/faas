package ociarchive

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// A redeploy reuses the layers of the app's previous uploaded image
// (ADR-20261127120000 amendment 1): the CLI uploads a thin archive, the
// canonical archive without the layer blobs the base already has, and apid
// rebuilds the full canonical archive from the thin upload and the base.

// ErrBaseMissing marks a layer that is in neither the thin archive nor the
// base, for example because another deploy replaced the base meanwhile.
var ErrBaseMissing = errors.New("image base lacks a layer the upload left out")

// Thin copies the canonical archive r to out without the layer blobs that
// reuse lists with the same size (digest → size). Only blobs the manifest
// names as layers are left out; oci-layout, index.json, the manifest and the
// config always stay. It returns how many layer blobs and bytes it left out.
func Thin(r io.Reader, out io.Writer, reuse map[string]int64) (int, int64, error) {
	tr := tar.NewReader(r)
	tw := tar.NewWriter(out)
	var manifestDigest string
	layers := map[string]bool{}
	omitted, omittedBytes := 0, int64(0)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, 0, fmt.Errorf("read image archive: %w", err)
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		digest := ""
		if m := blobName.FindStringSubmatch(name); m != nil {
			digest = "sha256:" + m[1]
		}
		if digest != "" && layers[digest] {
			if size, ok := reuse[digest]; ok && size == hdr.Size {
				omitted++
				omittedBytes += hdr.Size
				continue
			}
		}
		// index.json and the manifest are small and come before the layers
		// in the canonical order: read them to learn which blobs are layers.
		if name == "index.json" || (digest != "" && digest == manifestDigest) {
			body, err := readBounded(tr, api.LocalOCIMaxManifestBytes)
			if err != nil {
				return 0, 0, fmt.Errorf("read %s: %w", name, err)
			}
			if name == "index.json" {
				var idx index
				if json.Unmarshal(body, &idx) == nil && len(idx.Manifests) == 1 {
					manifestDigest = idx.Manifests[0].Digest
				}
			} else {
				var m oci.Manifest
				if json.Unmarshal(body, &m) == nil {
					for _, l := range m.Layers {
						layers[l.Digest] = true
					}
				}
			}
			if err := writeEntry(tw, hdr.Name, int64(len(body)), strings.NewReader(string(body))); err != nil {
				return 0, 0, err
			}
			continue
		}
		if err := writeEntry(tw, hdr.Name, hdr.Size, tr); err != nil {
			return 0, 0, err
		}
	}
	if err := tw.Close(); err != nil {
		return 0, 0, fmt.Errorf("finish thin archive: %w", err)
	}
	return omitted, omittedBytes, nil
}

// blobSpan is where a blob's bytes lie in a base archive.
type blobSpan struct{ offset, size int64 }

// Base is an indexed canonical archive whose blobs Rebuild copies from.
type Base struct {
	r     io.ReaderAt
	blobs map[string]blobSpan
}

// countingReader tracks how far tar has read, which after Next is where the
// entry's data starts: archive/tar reads whole 512-byte blocks, no further.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// IndexBase records where each blob of a base archive lies. It reads every
// header and skips the data; Rebuild's output is inspected afterwards, which
// re-hashes every blob it copied.
func IndexBase(r io.ReaderAt, size int64) (*Base, error) {
	cr := &countingReader{r: io.NewSectionReader(r, 0, size)}
	tr := tar.NewReader(cr)
	b := &Base{r: r, blobs: map[string]blobSpan{}}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return b, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read image base: %w", err)
		}
		if m := blobName.FindStringSubmatch(strings.TrimPrefix(hdr.Name, "./")); m != nil && hdr.Typeflag == tar.TypeReg {
			b.blobs["sha256:"+m[1]] = blobSpan{offset: cr.n, size: hdr.Size}
		}
	}
}

// Rebuild writes the full canonical archive from a thin upload and the base:
// every entry of the thin archive in its order, with each layer the thin
// archive left out copied from the base where the canonical order puts it.
// It returns how many layers it took from the base. The result must still be
// inspected; Rebuild checks only that the pieces fit together.
func Rebuild(thin io.Reader, base *Base, out io.Writer) (int, error) {
	tr := tar.NewReader(thin)
	tw := tar.NewWriter(out)
	var manifestDigest, configDigest string
	var layers []oci.Descriptor // deduplicated, in manifest order
	next, fromBase := 0, 0
	seen := map[string]bool{}
	copyFromBase := func(d oci.Descriptor) error {
		span, ok := base.blobs[d.Digest]
		if !ok || span.size != d.Size {
			return fmt.Errorf("%w: %s", ErrBaseMissing, d.Digest)
		}
		fromBase++
		return writeEntry(tw, blobPath(d.Digest), span.size, io.NewSectionReader(base.r, span.offset, span.size))
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, invalid("read tar: %v", err)
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		m := blobName.FindStringSubmatch(name)
		if m == nil || manifestDigest == "" || len(layers) == 0 || "sha256:"+m[1] == configDigest {
			// Metadata comes first: oci-layout, index.json, manifest, config.
			body, err := readBounded(tr, api.LocalOCIMaxConfigBytes)
			if err != nil {
				return 0, invalid("%s: %v", name, err)
			}
			if name == "index.json" {
				var idx index
				if json.Unmarshal(body, &idx) == nil && len(idx.Manifests) == 1 {
					manifestDigest = idx.Manifests[0].Digest
				}
			} else if m != nil && "sha256:"+m[1] == manifestDigest {
				var man oci.Manifest
				if err := json.Unmarshal(body, &man); err != nil {
					return 0, invalid("decode manifest: %v", err)
				}
				configDigest = man.Config.Digest
				for _, l := range man.Layers {
					if !seen[l.Digest] {
						seen[l.Digest] = true
						layers = append(layers, l)
					}
				}
				if len(layers) == 0 {
					return 0, invalid("image has no layers")
				}
			}
			if err := writeEntry(tw, name, int64(len(body)), strings.NewReader(string(body))); err != nil {
				return 0, err
			}
			continue
		}
		digest := "sha256:" + m[1]
		// Layers the thin archive left out come before this one.
		for next < len(layers) && layers[next].Digest != digest {
			if err := copyFromBase(layers[next]); err != nil {
				return 0, err
			}
			next++
		}
		if next == len(layers) {
			return 0, invalid("blob %s is not a layer of the image, or is out of order", digest)
		}
		next++
		if err := writeEntry(tw, name, hdr.Size, tr); err != nil {
			return 0, err
		}
	}
	if manifestDigest == "" || len(layers) == 0 {
		return 0, invalid("the upload has no image manifest")
	}
	for ; next < len(layers); next++ {
		if err := copyFromBase(layers[next]); err != nil {
			return 0, err
		}
	}
	if err := tw.Close(); err != nil {
		return 0, fmt.Errorf("finish image archive: %w", err)
	}
	return fromBase, nil
}
