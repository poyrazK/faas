package imaged

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// Open once for all archive passes. Reopening a path for each component could
// combine an index from one export with blobs from a replacement export.
func openLocalOCIArchive(path string) (*os.File, error) {
	//nolint:forbidigo // internal builderd output selected from the deployment row.
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open OCI archive: %w", err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("stat OCI archive: %w", err)
		}
		return nil, errors.New("OCI archive is not a regular file")
	}
	if info.Size() > api.LocalOCIMaxArchiveBytes {
		_ = f.Close()
		return nil, errors.New("OCI archive exceeds size limit")
	}
	return f, nil
}

type localOCIContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r localOCIContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Keep tar's ability to seek over unrelated blobs during metadata passes;
// cancellation applies to both reads and seeks on the same opened archive.
type localOCIContextReadSeeker struct {
	localOCIContextReader
	archive io.ReadSeeker
}

func (r localOCIContextReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.archive.Seek(offset, whence)
}

func localOCITarReader(ctx context.Context, archive io.ReadSeeker) (*tar.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind OCI archive: %w", err)
	}
	return tar.NewReader(localOCIContextReadSeeker{localOCIContextReader{ctx, archive}, archive}), nil
}

func readLocalOCIEntryFrom(ctx context.Context, archive io.ReadSeeker, name string, maxBytes int64) ([]byte, error) {
	tr, err := localOCITarReader(ctx, archive)
	if err != nil {
		return nil, err
	}
	var data []byte
	seen := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name != name {
			continue
		}
		if seen {
			return nil, fmt.Errorf("duplicate OCI entry %q", name)
		}
		if err := validateLocalOCIEntry(hdr, maxBytes); err != nil {
			return nil, err
		}
		seen = true
		data, err = io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
	}
	if !seen {
		return nil, fmt.Errorf("entry %q not found", name)
	}
	return data, nil
}

func validateLocalOCIEntry(hdr *tar.Header, maxBytes int64) error {
	if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
		return fmt.Errorf("OCI entry %q is not a regular file", hdr.Name)
	}
	if hdr.Size < 0 || hdr.Size > maxBytes {
		return fmt.Errorf("entry %q exceeds %d bytes", hdr.Name, maxBytes)
	}
	return nil
}

func readLocalOCIDescriptor(ctx context.Context, archive io.ReadSeeker, desc oci.Descriptor, maxBytes int64) ([]byte, error) {
	name, err := localOCIBlobName(desc.Digest)
	if err != nil {
		return nil, err
	}
	if desc.Size < 0 || desc.Size > maxBytes {
		return nil, fmt.Errorf("OCI descriptor size outside 0..%d bytes", maxBytes)
	}
	body, err := readLocalOCIEntryFrom(ctx, archive, name, maxBytes)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != desc.Size || localOCIDigest(body) != desc.Digest {
		return nil, fmt.Errorf("OCI blob %q does not match its descriptor", name)
	}
	return body, nil
}

func localOCIDigest(body []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(body))
}

func validateLocalOCIManifest(desc oci.Descriptor, manifest oci.Manifest) error {
	if !localOCIManifestMediaType(desc.MediaType) || !localOCIManifestMediaType(manifest.MediaType) || manifest.SchemaVersion != 2 {
		return errors.New("unsupported OCI image manifest format")
	}
	if manifest.Config.MediaType != "application/vnd.oci.image.config.v1+json" && manifest.Config.MediaType != "application/vnd.docker.container.image.v1+json" {
		return errors.New("unsupported OCI config media type")
	}
	if _, err := localOCIBlobName(manifest.Config.Digest); err != nil {
		return fmt.Errorf("OCI config digest: %w", err)
	}
	if manifest.Config.Size < 0 || manifest.Config.Size > api.LocalOCIMaxConfigBytes {
		return errors.New("OCI config exceeds size limit")
	}
	if len(manifest.Layers) > api.LocalOCIMaxLayers {
		return errors.New("OCI image exceeds layer count limit")
	}
	var total int64
	for i, layer := range manifest.Layers {
		if _, err := localOCIBlobName(layer.Digest); err != nil {
			return fmt.Errorf("OCI layer %d digest: %w", i, err)
		}
		if layer.MediaType != "application/vnd.oci.image.layer.v1.tar+gzip" && layer.MediaType != "application/vnd.docker.image.rootfs.diff.tar.gzip" {
			return fmt.Errorf("unsupported OCI layer %d media type", i)
		}
		if layer.Size < 0 || layer.Size > api.LocalOCIMaxCompressedLayerBytes-total {
			return errors.New("OCI layers exceed compressed size limit")
		}
		total += layer.Size
	}
	return nil
}

func localOCIManifestMediaType(mediaType string) bool {
	return mediaType == "application/vnd.oci.image.manifest.v1+json" || mediaType == "application/vnd.docker.distribution.manifest.v2+json"
}

type localOCIBlobTarget struct {
	desc    oci.Descriptor
	writers []io.Writer
	seen    bool
}

func localOCIBlobTargets(manifest oci.Manifest, files []*os.File, config *bytes.Buffer) (map[string]*localOCIBlobTarget, error) {
	name, _ := localOCIBlobName(manifest.Config.Digest) // descriptors validated before allocation
	targets := map[string]*localOCIBlobTarget{name: {desc: manifest.Config, writers: []io.Writer{config}}}
	for i, layer := range manifest.Layers {
		name, _ := localOCIBlobName(layer.Digest)
		if target, ok := targets[name]; ok {
			if target.desc != layer {
				return nil, fmt.Errorf("conflicting OCI descriptors for %q", name)
			}
			// A valid image may use the same layer at multiple positions. Each
			// position receives its own reader; the archive must contain one blob.
			target.writers = append(target.writers, files[i])
		} else {
			targets[name] = &localOCIBlobTarget{desc: layer, writers: []io.Writer{files[i]}}
		}
	}
	return targets, nil
}

func extractVerifiedLocalOCIBlobs(ctx context.Context, archive io.ReadSeeker, manifest oci.Manifest, files []*os.File) ([]byte, error) {
	var config bytes.Buffer
	targets, err := localOCIBlobTargets(manifest, files, &config)
	if err != nil {
		return nil, err
	}
	tr, err := localOCITarReader(ctx, archive)
	if err != nil {
		return nil, err
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if target := targets[hdr.Name]; target != nil {
			if err := extractLocalOCIBlob(tr, hdr, target); err != nil {
				return nil, err
			}
		}
	}
	for name, target := range targets {
		if !target.seen {
			return nil, fmt.Errorf("OCI blob %q not found", name)
		}
	}
	return config.Bytes(), nil
}

func extractLocalOCIBlob(tr *tar.Reader, hdr *tar.Header, target *localOCIBlobTarget) error {
	if target.seen {
		return fmt.Errorf("duplicate OCI blob entry %q", hdr.Name)
	}
	if err := validateLocalOCIEntry(hdr, target.desc.Size); err != nil {
		return err
	}
	if hdr.Size != target.desc.Size {
		return fmt.Errorf("OCI blob %q size does not match its descriptor", hdr.Name)
	}
	h := sha256.New()
	writers := append([]io.Writer{h}, target.writers...)
	n, err := io.Copy(io.MultiWriter(writers...), tr)
	if err != nil {
		return fmt.Errorf("extract OCI blob %q: %w", hdr.Name, err)
	}
	if n != target.desc.Size || fmt.Sprintf("sha256:%x", h.Sum(nil)) != target.desc.Digest {
		return fmt.Errorf("OCI blob %q digest does not match its descriptor", hdr.Name)
	}
	target.seen = true
	return nil
}

// DiffIDs authenticate the entire uncompressed stream, including the gzip
// trailer and bytes beyond tar's end marker. Hash before rootfs conversion or
// layer filtering, so prefix decisions cannot trust invented config DiffIDs.
func verifyLocalOCIDiffIDs(ctx context.Context, files []*os.File, diffIDs []string, maxBytes int64) error {
	var total int64
	for i, file := range files {
		if _, err := localOCIBlobName(diffIDs[i]); err != nil {
			return fmt.Errorf("OCI layer %d DiffID: %w", i, err)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		zr, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("open OCI layer %d gzip: %w", i, err)
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, io.LimitReader(localOCIContextReader{ctx, zr}, maxBytes-total+1))
		closeErr := zr.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return fmt.Errorf("verify OCI layer %d gzip: %w", i, err)
		}
		if n > maxBytes-total {
			return errors.New("OCI layers exceed uncompressed size limit")
		}
		if fmt.Sprintf("sha256:%x", h.Sum(nil)) != diffIDs[i] {
			return fmt.Errorf("OCI layer %d does not match its uncompressed DiffID", i)
		}
		total += n
	}
	return nil
}
