package releaseinstall

// adr: 20261119090000

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// StagedTarball is a release tarball decompressed once into a private
// staging directory (ADR-20261119090000). The canonical install used to
// decompress the 480 MB tarball three times: to find the manifest, for the
// hash walk, and to extract, about 26 s each on a 4-vCPU compute node. Staging
// reads it once; VerifyStaged and PublishStaged then work from the staged
// files and their hashes.
type StagedTarball struct {
	dir string
	// files maps each regular member's cleaned archive name to its staged
	// path; hashes maps it to the member's sha256.
	files    map[string]string
	hashes   map[string]string
	manifest []byte
}

// StageTarball decompresses packed once into a new private directory under
// parent (the releases root, so publishing is a rename on one filesystem).
// Nothing in the archive is trusted yet: members are staged under numbered
// names, never their archive names, and only VerifyStaged decides whether
// they match the signed manifest. The caller must Close the result.
func StageTarball(packed []byte, parent string) (*StagedTarball, error) {
	dir, err := os.MkdirTemp(parent, ".stage-")
	if err != nil {
		return nil, fmt.Errorf("releaseinstall: stage dir: %w", err)
	}
	s := &StagedTarball{dir: dir, files: map[string]string{}, hashes: map[string]string{}}
	if err := s.stage(packed); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *StagedTarball) stage(packed []byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return fmt.Errorf("%w: gzip reader: %w", ErrTarballTampered, err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for n := 0; ; n++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: tar next: %w", ErrTarballTampered, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name, err := SafeArchiveRelativeName(filepath.ToSlash(hdr.Name))
		if err != nil {
			return fmt.Errorf("%w: unsafe tar entry %q", ErrTarballTampered, hdr.Name)
		}
		if name == ManifestName {
			body, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes+1))
			if err != nil {
				return fmt.Errorf("%w: tar read %s: %w", ErrTarballTampered, hdr.Name, err)
			}
			if len(body) > maxManifestBytes {
				return fmt.Errorf("%w: %s larger than %d bytes", ErrTarballTampered, ManifestName, maxManifestBytes)
			}
			s.manifest = body
			sum := sha256.Sum256(body)
			s.hashes[name] = hex.EncodeToString(sum[:])
			continue
		}
		staged := filepath.Join(s.dir, strconv.Itoa(n))
		sum, err := writeStaged(staged, tr)
		if err != nil {
			return fmt.Errorf("releaseinstall: stage %s: %w", hdr.Name, err)
		}
		s.files[name] = staged
		s.hashes[name] = sum
	}
}

// maxManifestBytes bounds the manifest member read into memory.
const maxManifestBytes = 16 << 20

// writeStaged copies r to a new owner-only file at path, synced, and returns
// its sha256.
func writeStaged(path string, r io.Reader) (string, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), r); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Manifest returns the release-manifest.json member, nil when the archive
// had none.
func (s *StagedTarball) Manifest() []byte { return s.manifest }

// Close removes the staging directory and anything left in it.
func (s *StagedTarball) Close() error {
	if s == nil || s.dir == "" {
		return nil
	}
	return os.RemoveAll(s.dir)
}

// VerifyStaged is Verify against a staged tarball: the same catalog hash walk
// over the hashes computed while staging, then the cosign signature over
// t.Packed. It never decompresses again.
func (t *Tarball) VerifyStaged(ctx context.Context, verifier CosignVerifier, s *StagedTarball) (string, error) {
	if t == nil || s == nil {
		return "", errors.New("releaseinstall: nil tarball or staging")
	}
	if verifier == nil {
		return "", errors.New("releaseinstall: VerifyStaged requires a non-nil CosignVerifier")
	}
	if err := ValidateManifest(t.Manifest); err != nil {
		return "", fmt.Errorf("releaseinstall: tarball manifest: %w", err)
	}
	if err := t.hashWalkWith(s.hashes); err != nil {
		return "", err
	}
	return t.verifySignature(ctx, verifier)
}

// PublishStaged is Extract from a verified staging: each member the
// manifest allows is renamed into <root>/<git-sha>/bin under its trusted
// catalog name, with Extract's containment checks. A rename leaves a running
// process on its old inode, as Extract's replaceFile does. Call it only after
// VerifyStaged succeeded.
func (t *Tarball) PublishStaged(root string, s *StagedTarball) error {
	if t == nil || s == nil {
		return errors.New("releaseinstall: nil tarball or staging")
	}
	if err := ValidateManifest(t.Manifest); err != nil {
		return fmt.Errorf("releaseinstall: publish validate: %w", err)
	}
	bin := BinDir(root, t.GitSHA)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return fmt.Errorf("releaseinstall: publish mkdir %s: %w", bin, err)
	}
	allowed := trustedArchiveEntries(t.Manifest)
	for name, staged := range s.files {
		safe, ok := allowed[name]
		if !ok {
			return fmt.Errorf("%w: unexpected archive entry %q", ErrTarballTampered, name)
		}
		outPath, err := containedBinPath(bin, safe, name)
		if err != nil {
			return err
		}
		if err := os.Chmod(staged, 0o755); err != nil {
			return fmt.Errorf("releaseinstall: publish chmod %s: %w", name, err)
		}
		if err := os.Rename(staged, outPath); err != nil {
			return fmt.Errorf("releaseinstall: publish %s: %w", outPath, err)
		}
	}
	return nil
}

// containedBinPath returns where the trusted catalog name safe goes under
// bin, refusing a symlinked bin directory, a parent that resolves outside
// it, or a target that is a symlink (Extract's checks).
func containedBinPath(bin, safe, archiveName string) (string, error) {
	binInfo, err := os.Lstat(bin)
	if err != nil {
		return "", fmt.Errorf("releaseinstall: lstat bin: %w", err)
	}
	if binInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: bin directory is a symlink", ErrTarballTampered)
	}
	binReal, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return "", fmt.Errorf("releaseinstall: eval bin symlinks: %w", err)
	}
	outPath := filepath.Join(bin, filepath.FromSlash(safe))
	parent := filepath.Dir(outPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("releaseinstall: publish mkdir %s: %w", parent, err)
	}
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("releaseinstall: eval publish parent: %w", err)
	}
	if parentReal != filepath.Clean(filepath.Join(binReal, filepath.Dir(filepath.FromSlash(safe)))) {
		return "", fmt.Errorf("%w: tarball entry %q escapes bin dir (%s)", ErrTarballTampered, archiveName, parentReal)
	}
	if info, err := os.Lstat(outPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: tarball entry %q targets a symlink", ErrTarballTampered, archiveName)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("releaseinstall: lstat publish path %s: %w", outPath, err)
	}
	return outPath, nil
}
