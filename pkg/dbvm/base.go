package dbvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A database VM's read-only base drive is a PostgreSQL image with
// guest-init as PID 1 (ADR-1027). vmmd builds it on the database node from a
// digest-pinned image, so what runs is exactly what the operator pinned.

// PostgresImages are the PostgreSQL images this release qualifies, by
// major: Debian bookworm PostgreSQL images pinned to their linux/amd64
// manifests. PostgreSQL 16 through 18 include pgvector. The database_vmmd role's
// defaults carry the same pins (a test keeps them equal). Guest-init runs
// every major the same way.
var PostgresImages = map[int]string{
	15: "docker.io/library/postgres@sha256:bbbf67eadd2381a2738a4a2e36d4c3936c36edd82d80e7ef00411194385d3829",
	// pgvector/pgvector:0.8.7-pg16-bookworm (PostgreSQL 16.16).
	16: "docker.io/pgvector/pgvector@sha256:018b4a92e634c3a5715f613dd43497e3d8028ae051be7ba6fd127bcffb0e7957",
	// pgvector/pgvector:0.8.7-pg17-bookworm (PostgreSQL 17.12).
	17: "docker.io/pgvector/pgvector@sha256:0a547a6067fdea4eec4cfb0887c4ddae2e91b1932cecc071541c14ee318312ce",
	// pgvector/pgvector:0.8.7-pg18-bookworm (PostgreSQL 18.7).
	18: "docker.io/pgvector/pgvector@sha256:4bdc16048580b56345bca441bffe8d9b45dd2f5d76963808c853df1661df6e00",
}

// pinnedImage requires a single-platform manifest digest: base staging
// rejects manifest lists, and a tag could move under a running fleet.
var pinnedImage = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?/[a-z0-9._/-]+@sha256:[0-9a-f]{64}$`)

// ValidateImage reports whether image is a digest-pinned reference.
func ValidateImage(image string) error {
	if !pinnedImage.MatchString(image) {
		return fmt.Errorf("dbvm: image %q must be pinned to a linux/amd64 manifest digest (registry/repository@sha256:...)", image)
	}
	return nil
}

// FileDigest is the hex sha256 of a file, used to key bases on the
// guest-init binary they embed.
func FileDigest(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec,forbidigo // the release guest-init path from vmmd.toml, never a customer path
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// BasePath names the base built from image with a guest-init of the given
// digest. The name changes with either input, so a release that ships a new
// guest-init gets a new base while running VMs keep theirs.
func BasePath(dir string, major int, image, guestInitDigest string) string {
	sum := sha256.Sum256([]byte(image + "\x00" + guestInitDigest))
	return filepath.Join(dir, fmt.Sprintf("postgres-%d-%s.ext4", major, hex.EncodeToString(sum[:8])))
}

// BuildBase pulls image and writes the base drive to out, atomically: a
// failed or interrupted build never leaves a file at out.
func BuildBase(ctx context.Context, image, guestInit, out string) error {
	if err := ValidateImage(image); err != nil {
		return err
	}
	client := oci.NewRegistryClient()
	manifest, err := client.PullManifest(ctx, image)
	if err != nil {
		return fmt.Errorf("dbvm: pull manifest %s: %w", image, err)
	}
	ref, err := oci.ParseReference(image)
	if err != nil {
		return fmt.Errorf("dbvm: %w", err)
	}
	readers := make([]io.Reader, 0, len(manifest.Layers))
	for _, layer := range manifest.Layers {
		body, err := client.PullBlob(ctx, ref.Registry+"/"+ref.Repository, layer.Digest)
		if err != nil {
			return fmt.Errorf("dbvm: pull layer %s: %w", layer.Digest, err)
		}
		defer func() { _ = body.Close() }()
		readers = append(readers, body)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("dbvm: base dir: %w", err)
	}
	tmp := out + ".building"
	_ = os.Remove(tmp)
	if _, err := rootfs.NewBuilder(wire.ExecRunner{}).BuildBase(ctx, rootfs.BaseBuildInput{Layers: readers, OutImage: tmp, GuestInitPath: guestInit}); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("dbvm: build base: %w", err)
	}
	if err := os.Chmod(tmp, 0o444); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("dbvm: publish base: %w", err)
	}
	return nil
}

// PruneBases removes the bases of major in dir other than keep, and any
// build a crash left behind. Running VMs are unaffected: their base is
// bind-mounted into the jail.
func PruneBases(dir string, major int, keep string) error {
	matches, err := filepath.Glob(filepath.Join(dir, fmt.Sprintf("postgres-%d-*.ext4*", major)))
	if err != nil {
		return err
	}
	for _, path := range matches {
		if path != keep {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
