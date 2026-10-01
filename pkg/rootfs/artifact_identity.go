package rootfs

// adr: 393

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/storage"
)

// ArtifactIdentity covers the complete ext4 stream, including metadata and
// unused capacity. Staged ContentBytes cannot stand in for this identity.
type ArtifactIdentity struct {
	Digest string
	Bytes  int64
}

type artifactIdentityReader struct {
	ctx    context.Context
	input  io.Reader
	digest hash.Hash
	bytes  int64
	eof    bool
}

func (r *artifactIdentityReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.input.Read(p)
	if n > 0 {
		_, _ = r.digest.Write(p[:n])
		r.bytes += int64(n)
	}
	if err == io.EOF {
		r.eof = true
	}
	return n, err
}
func (r *artifactIdentityReader) identity() (ArtifactIdentity, error) {
	if err := r.ctx.Err(); err != nil {
		return ArtifactIdentity{}, err
	}
	if !r.eof || r.bytes <= 0 {
		return ArtifactIdentity{}, fmt.Errorf("rootfs: complete nonempty artifact stream required")
	}
	return ArtifactIdentity{Digest: fmt.Sprintf("sha256:%x", r.digest.Sum(nil)), Bytes: r.bytes}, nil
}

// ReadArtifactIdentity hashes a complete storage read. The caller owns/ closes
// the reader. This alone proves neither conversion lineage nor publisher trust.
func ReadArtifactIdentity(ctx context.Context, r io.Reader) (ArtifactIdentity, error) {
	tracked := &artifactIdentityReader{ctx: ctx, input: r, digest: sha256.New()}
	if _, err := io.Copy(io.Discard, tracked); err != nil {
		return ArtifactIdentity{}, err
	}
	return tracked.identity()
}

func publishArtifactIdentity(ctx context.Context, be storage.StorageBackend, key string, f *os.File) (ArtifactIdentity, error) {
	info, err := f.Stat()
	if err != nil {
		return ArtifactIdentity{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return ArtifactIdentity{}, fmt.Errorf("rootfs: regular nonempty mkfs output required")
	}
	tracked := &artifactIdentityReader{ctx: ctx, input: f, digest: sha256.New()}
	if err := be.Put(ctx, key, tracked); err != nil {
		return ArtifactIdentity{}, err
	}
	if err := ctx.Err(); err != nil {
		return ArtifactIdentity{}, err
	}
	// A backend may consume exactly the advertised length without probing EOF.
	// Probe once; unread bytes mean it did not publish the complete input.
	if !tracked.eof {
		var probe [1]byte
		n, err := tracked.Read(probe[:])
		if err != nil && err != io.EOF {
			return ArtifactIdentity{}, fmt.Errorf("rootfs: complete artifact probe: %w", err)
		}
		if n != 0 || err != io.EOF {
			return ArtifactIdentity{}, fmt.Errorf("rootfs: storage did not consume complete artifact: %w", io.ErrUnexpectedEOF)
		}
	}
	value, err := tracked.identity()
	if err != nil {
		return ArtifactIdentity{}, err
	}
	if value.Bytes != info.Size() {
		return ArtifactIdentity{}, fmt.Errorf("rootfs: mkfs output changed during publication")
	}
	return value, nil
}

func artifactIdentityFromPath(ctx context.Context, path string) (ArtifactIdentity, error) {
	f, err := os.Open(path) //nolint:forbidigo // daemon-owned mkfs output
	if err != nil {
		return ArtifactIdentity{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ArtifactIdentity{}, err
	}
	if !info.Mode().IsRegular() {
		return ArtifactIdentity{}, fmt.Errorf("rootfs: regular mkfs output required")
	}
	identity, err := ReadArtifactIdentity(ctx, f)
	if err != nil {
		return ArtifactIdentity{}, err
	}
	if identity.Bytes != info.Size() {
		return ArtifactIdentity{}, fmt.Errorf("rootfs: mkfs output changed while hashing")
	}
	return identity, nil
}
