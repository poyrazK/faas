package vmmdmount

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeScanTargetPinsOutputWithoutFollowingReplacement(t *testing.T) {
	parent := t.TempDir()
	path, err := os.MkdirTemp(parent, RuntimeScanTargetPrefix)
	if err != nil {
		t.Fatal(err)
	}
	target, err := openRuntimeScanTarget(path, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	if err := os.Symlink(foreign, path); err != nil {
		t.Fatal(err)
	}
	view, err := target.CreateView("main")
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Close(); err != nil {
		t.Fatal(err)
	}
	if err := target.Verify(); err == nil {
		t.Fatal("replaced output pathname acquired receipt authority")
	}
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
		t.Fatal("native output followed replacement outside pinned target", err)
	}
	if _, err := os.Stat(filepath.Join(path+"-old", "main")); err != nil {
		t.Fatal("write did not remain in original pinned directory", err)
	}
}

func TestRuntimeScanTargetRefusesLostPrivacyBeforeReceipt(t *testing.T) {
	parent := t.TempDir()
	path, err := os.MkdirTemp(parent, RuntimeScanTargetPrefix)
	if err != nil {
		t.Fatal(err)
	}
	target, err := openRuntimeScanTarget(path, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := target.Verify(); err == nil {
		t.Fatal("shared output retained receipt authority")
	}
}

func TestRuntimeScanTargetRefusesUnsafeDirectories(t *testing.T) {
	for _, mode := range []string{"foreign name", "nested", "symlink", "file", "nonempty", "shared mode", "parent symlink"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			path, err := os.MkdirTemp(parent, RuntimeScanTargetPrefix)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "foreign name":
				path = parent
			case "nested":
				path = filepath.Join(path, RuntimeScanTargetPrefix+"nested")
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink", "file":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if mode == "file" {
					err = os.WriteFile(path, nil, 0600)
				} else {
					err = os.Symlink(parent, path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "nonempty":
				if err := os.WriteFile(filepath.Join(path, "extra"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "shared mode":
				if err := os.Chmod(path, 0755); err != nil {
					t.Fatal(err)
				}
			case "parent symlink":
				link := parent + "-link"
				if err := os.Symlink(parent, link); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(link) })
				parent = link
				path = filepath.Join(link, filepath.Base(path))
			}
			target, err := openRuntimeScanTarget(path, parent)
			if err == nil {
				_ = target.Close()
				t.Fatal("unsafe directory accepted", mode)
			}
		})
	}
}
