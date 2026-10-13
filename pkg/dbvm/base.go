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
