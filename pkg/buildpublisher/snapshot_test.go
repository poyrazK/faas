package buildpublisher

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotExportFreezesExactApprovedBytes(t *testing.T) {
	body := []byte("approved archive including padding\x00\x00")
	path := filepath.Join(t.TempDir(), "export.tar")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	s, err := SnapshotExport(t.Context(), path, digest, int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	private := s.Path()
	if err := os.WriteFile(path, []byte("different original"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(private)
	if err != nil || string(got) != string(body) {
		t.Fatal("snapshot changed with original", string(got), err)
	}
	for _, check := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Dir(private), 0700}, {private, 0400}} {
		info, err := os.Stat(check.path)
		if err != nil || info.Mode().Perm() != check.mode {
			t.Fatal("private export permissions", check.path, info, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(private)); !os.IsNotExist(err) {
		t.Fatal("snapshot directory retained", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal("close is not idempotent", err)
	}
}

func TestSnapshotExportRejectsUnapprovedOrSpecialInput(t *testing.T) {
	body := []byte("approved")
	path := filepath.Join(t.TempDir(), "export.tar")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	for _, tc := range []struct {
		name, path, digest string
		size               int64
	}{
		{"wrong digest", path, "sha256:" + strings.Repeat("0", 64), int64(len(body))},
		{"short", path, digest, int64(len(body) - 1)}, {"long", path, digest, int64(len(body) + 1)},
		{"invalid digest", path, "SHA256:" + strings.Repeat("0", 64), int64(len(body))},
		{"empty", path, digest, 0}, {"directory", filepath.Dir(path), digest, 8}, {"symlink", link, digest, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := SnapshotExport(t.Context(), tc.path, tc.digest, tc.size)
			if err == nil {
				_ = s.Close()
				t.Fatal("unapproved input accepted")
			}
			if s != nil {
				t.Fatal("failed snapshot exposed a path", s.Path())
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := SnapshotExport(ctx, path, digest, int64(len(body))); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if _, _, err := MeasureExport(t.Context(), link); err == nil {
		t.Fatal("publisher followed symlink")
	}
}

func TestSnapshotCopyRejectsLengthAndCleansFailedTemp(t *testing.T) {
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("approved")))
	for _, body := range []string{"approve", "approved-extra"} {
		if err := copyExpectedExport(t.Context(), strings.NewReader(body), filepath.Join(t.TempDir(), "out"), digest, 8); !errors.Is(err, ErrInvalid) {
			t.Fatal("length mismatch accepted", err)
		}
	}
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("approved"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SnapshotExport(t.Context(), path, "sha256:"+strings.Repeat("0", 64), 8); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed copy retained private directory", entries, err)
	}
}
