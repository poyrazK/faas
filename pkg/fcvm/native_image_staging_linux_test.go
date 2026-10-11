//go:build linux

// adr: 568 — file identity tests complement privileged staging crash recovery.
package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func nativeStagingPreparationFixture(t *testing.T) (*linuxNativeImagePreparation, string) {
	t.Helper()
	input := nativeWritableFileFixture(t, "immutable.img", "original-private-output")
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, input.Name())) {
		t.Skip("anonymous staging requires an ext4, XFS or Btrfs disk filesystem")
	}
	clone, err := cloneNativeStagedImage(t.Context(), input, filepath.Dir(input.Name()))
	if err != nil {
		t.Fatal(err)
	}
	identity, _, err := nativeImageFileMetadata(clone)
	if err != nil {
		_ = clone.Close()
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(root)
	if err != nil {
		_ = clone.Close()
		t.Fatal(err)
	}
	p := &linuxNativeImagePreparation{source: clone, root: directory, identity: identity}
	t.Cleanup(func() { _ = p.source.Close(); _ = directory.Close() })
	return p, filepath.Join(root, "original-epoch")
}

func TestNativeStagingSourceClosesWithNoNamedCopyOrChangedInode(t *testing.T) {
	p, point := nativeStagingPreparationFixture(t)
	if err := p.linkAnonymousSource(point); err != nil {
		t.Fatal(err)
	}
	identity, _, err := nativeImageFileMetadata(p.source)
	if err != nil || identity != p.identity || nativeWritableReadFile(t, p.source) != "original-private-output" {
		t.Fatal("temporary staging link changed original private input", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(p.source.Fd()), &stat); err != nil || stat.Nlink != 1 {
		t.Fatal("temporary source has an unexpected alias", stat.Nlink, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(point + nativeImageStagingSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed preparation retained a named private copy", err)
	}
}

func TestNativeStagingSourceCleanupPreservesSubstitutedOrAliasedFiles(t *testing.T) {
	for _, change := range []string{"replacement", "symlink", "additional_alias"} {
		t.Run(change, func(t *testing.T) {
			p, point := nativeStagingPreparationFixture(t)
			if err := p.linkAnonymousSource(point); err != nil {
				t.Fatal(err)
			}
			path := point + nativeImageStagingSuffix
			if change == "additional_alias" {
				if err := os.Link(path, point+"-foreign"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if change == "symlink" {
					if err := os.Symlink(p.source.Name(), path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Close(); err == nil {
				t.Fatal("changed temporary source supplied cleanup authority")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("uncertain cleanup removed the substituted source", err)
			}
		})
	}
}
