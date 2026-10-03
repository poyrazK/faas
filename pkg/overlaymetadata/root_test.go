package overlaymetadata

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadRootOpacityAcceptsOnlySupportedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, value      string
		readErr, wantErr error
		want             bool
	}{
		{name: "absent", readErr: ErrAbsent},
		{name: "opaque", value: "y", want: true},
		{name: "whiteout hint", value: "x"},
		{name: "empty", wantErr: ErrInvalid},
		{name: "long", value: "yy", wantErr: ErrInvalid},
		{name: "unsupported value", value: "n", wantErr: ErrInvalid},
		{name: "permission denied", readErr: syscall.EPERM, wantErr: syscall.EPERM},
		{name: "oversized", readErr: syscall.ERANGE, wantErr: syscall.ERANGE},
		{name: "unsupported filesystem", readErr: ErrUnsupported, wantErr: ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			got, err := readRootOpacity(root, func(path, name string, value []byte) (int, error) {
				if path != root || name != RootOpaqueXattr || len(value) != 2 {
					t.Fatal("reader changed the supported root, namespace or value bound")
				}
				return copy(value, tc.value), tc.readErr
			})
			if got != tc.want || tc.wantErr == nil && err != nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("opacity = %v, %v; want %v, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestReadRootOpacityRejectsInvalidOrReplacedRoot(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "missing", "replaced", "replaced absent"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "root")
			switch kind {
			case "file":
				if err := os.WriteFile(root, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(parent, root); err != nil {
					t.Fatal(err)
				}
			case "replaced", "replaced absent":
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			_, err := readRootOpacity(root, func(string, string, []byte) (int, error) {
				called = true
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "replaced absent" {
					return 0, ErrAbsent
				}
				return 1, nil
			})
			if !errors.Is(err, ErrInvalid) || called != (kind == "replaced" || kind == "replaced absent") {
				t.Fatalf("invalid root accepted: called=%v, err=%v", called, err)
			}
		})
	}
}

func TestEnsureEmptyLowerDirectoryRefusesImageContents(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "content", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "empty-lower")
			switch kind {
			case "empty", "content":
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "content" {
					if err := os.WriteFile(filepath.Join(root, "unexpected"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "file":
				if err := os.WriteFile(root, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(parent, root); err != nil {
					t.Fatal(err)
				}
			}
			err := EnsureEmptyLowerDirectory(root)
			if wantValid := kind == "missing" || kind == "empty"; wantValid != (err == nil) {
				t.Fatalf("empty lower accepted incorrectly: %v", err)
			}
		})
	}
}
