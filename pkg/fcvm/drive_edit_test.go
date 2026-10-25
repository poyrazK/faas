package fcvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/guestdisk"
)

// dirDriveImage models a guestdisk session over a plain directory so the
// pure-Go tier can run every drive stager without e2fsprogs. It follows the
// sandboxed editor's rules: no symlink is followed, a missing parent is
// created root-owned 0755, a non-directory parent or a directory target
// fails with *guestdisk.PathError, and a file the test process owns reads as
// root-owned (vmmd's own writes in production).
type dirDriveImage struct {
	root     string
	prepared int
}

func (d *dirDriveImage) host(p string) string { return filepath.Join(d.root, filepath.FromSlash(p)) }

func (d *dirDriveImage) Prepare(context.Context) (bool, error) {
	d.prepared++
	return false, nil
}

func (d *dirDriveImage) entry(p string, read *guestdisk.Read) guestdisk.Entry {
	comps := imageComponents(p)
	for _, dir := range comps[:len(comps)-1] {
		info, err := os.Lstat(d.host(dir))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return guestdisk.Entry{Kind: guestdisk.Missing}
			}
			return guestdisk.Entry{Kind: guestdisk.Unknown}
		}
		if !info.IsDir() {
			return guestdisk.Entry{Kind: guestdisk.Unknown}
		}
	}
	info, err := os.Lstat(d.host(p))
	if errors.Is(err, fs.ErrNotExist) {
		return guestdisk.Entry{Kind: guestdisk.Missing}
	}
	if err != nil {
		return guestdisk.Entry{Kind: guestdisk.Unknown}
	}
	e := guestdisk.Entry{Mode: uint32(info.Mode().Perm()), Size: info.Size()}
	if info.Mode()&fs.ModeSetuid != 0 {
		e.Mode |= 0o4000
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		e.UID, e.GID = int(st.Uid), int(st.Gid)
		if e.UID == os.Geteuid() {
			e.UID, e.GID = 0, 0
		}
	}
	switch {
	case info.Mode().IsRegular():
		e.Kind = guestdisk.Regular
		if read != nil && e.Size <= read.Max {
			if data, err := os.ReadFile(d.host(p)); err == nil {
				e.Data = data
			}
		}
	case info.IsDir():
		e.Kind = guestdisk.Directory
	case info.Mode()&fs.ModeSymlink != 0:
		e.Kind = guestdisk.Symlink
	default:
		e.Kind = guestdisk.Other
	}
	return e
}

func (d *dirDriveImage) Inspect(_ context.Context, paths []string, reads map[string]guestdisk.Read) (map[string]guestdisk.Entry, error) {
	out := make(map[string]guestdisk.Entry, len(paths))
	for _, p := range paths {
		if err := guestdisk.CheckPath(p); err != nil {
			return nil, err
		}
		var read *guestdisk.Read
		if r, ok := reads[p]; ok {
			read = &r
		}
		out[p] = d.entry(p, read)
	}
	return out, nil
}

func (d *dirDriveImage) WriteFilesAfter(_ context.Context, files []guestdisk.File, _ map[string]guestdisk.Entry) error {
	for _, f := range files {
		if err := guestdisk.CheckPath(f.Path); err != nil {
			return &guestdisk.PathError{Path: f.Path, Err: err}
		}
		comps := imageComponents(f.Path)
		for _, dir := range comps[:len(comps)-1] {
			switch e := d.entry(dir, nil); e.Kind {
			case guestdisk.Directory:
			case guestdisk.Missing:
				if err := os.Mkdir(d.host(dir), 0o755); err != nil {
					return &guestdisk.PathError{Path: f.Path, Err: err}
				}
			default:
				return &guestdisk.PathError{Path: f.Path, Err: fmt.Errorf("%s is a %s: %w", dir, e.Kind, guestdisk.ErrNotDirectory)}
			}
		}
		mode := fs.FileMode(f.Mode)
		switch e := d.entry(f.Path, nil); e.Kind {
		case guestdisk.Missing:
		case guestdisk.Directory:
			return &guestdisk.PathError{Path: f.Path, Err: guestdisk.ErrIsDirectory}
		case guestdisk.Regular:
			if f.KeepAttrs {
				mode = fs.FileMode(e.Mode & 0o777)
			}
			fallthrough
		default:
			if err := os.Remove(d.host(f.Path)); err != nil {
				return &guestdisk.PathError{Path: f.Path, Err: err}
			}
		}
		if err := os.WriteFile(d.host(f.Path), f.Data, mode); err != nil {
			return &guestdisk.PathError{Path: f.Path, Err: err}
		}
		if err := os.Chmod(d.host(f.Path), mode); err != nil {
			return &guestdisk.PathError{Path: f.Path, Err: err}
		}
	}
	return nil
}

func (d *dirDriveImage) RemoveAll(_ context.Context, paths []string) error {
	for _, p := range paths {
		comps := imageComponents(p)
		reachable := true
		for _, dir := range comps[:len(comps)-1] {
			if d.entry(dir, nil).Kind != guestdisk.Directory {
				reachable = false
			}
		}
		if reachable {
			if err := os.RemoveAll(d.host(p)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *dirDriveImage) List(_ context.Context, dirs []string, maxEntries int) (map[string][]guestdisk.DirEntry, error) {
	out := make(map[string][]guestdisk.DirEntry, len(dirs))
	for _, dir := range dirs {
		entries, err := os.ReadDir(d.host(dir))
		if err != nil {
			continue
		}
		if len(entries) > maxEntries {
			return nil, errors.New("too many entries")
		}
		for _, e := range entries {
			kind := guestdisk.Other
			switch {
			case e.Type().IsRegular():
				kind = guestdisk.Regular
			case e.IsDir():
				kind = guestdisk.Directory
			case e.Type()&fs.ModeSymlink != 0:
				kind = guestdisk.Symlink
			}
			out[dir] = append(out[dir], guestdisk.DirEntry{Name: e.Name(), Kind: kind})
		}
	}
	return out, nil
}

// DumpTree copies like debugfs rdump: directories, regular files, and
// symlinks recreated as symlinks.
func (d *dirDriveImage) DumpTree(_ context.Context, dir, dest string) (string, error) {
	src := d.host(dir)
	out := filepath.Join(dest, filepath.Base(src))
	err := filepath.WalkDir(src, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(out, rel)
		switch {
		case e.IsDir():
			return os.MkdirAll(target, 0o755)
		case e.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case e.Type().IsRegular():
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer func() { _ = in.Close() }()
			o, err := os.Create(target)
			if err != nil {
				return err
			}
			_, err = io.Copy(o, in)
			return errors.Join(err, o.Close())
		}
		return nil
	})
	return out, err
}

func (d *dirDriveImage) Close() error { return nil }

// fakeDriveImages swaps openDriveImage for a directory so the pure-Go tier
// can count drive sessions and inspect what each one wrote. The drive file
// itself must exist, as for the real editor.
func fakeDriveImages(t *testing.T) (*int, *string) {
	t.Helper()
	sessions := 0
	root := t.TempDir()
	prev := openDriveImage
	openDriveImage = func(_ *JailerVMM, drive string) (driveImage, error) {
		if _, err := os.Stat(drive); err != nil {
			return nil, err
		}
		sessions++
		return &dirDriveImage{root: root}, nil
	}
	t.Cleanup(func() { openDriveImage = prev })
	return &sessions, &root
}

// withDriveDir points every drive session at dir (one test's prepared
// drive contents).
func withDriveDir(t *testing.T, dir string) {
	t.Helper()
	prev := openDriveImage
	openDriveImage = func(*JailerVMM, string) (driveImage, error) { return &dirDriveImage{root: dir}, nil }
	t.Cleanup(func() { openDriveImage = prev })
}

// layoutOf inspects a directory drive the way a staging session does.
func layoutOf(t *testing.T, dir string, files []driveFile) driveLayout {
	t.Helper()
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	layout, err := inspectDrive(context.Background(), &dirDriveImage{root: dir}, paths, presentReads(files))
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestResolveGuestDiskHelperHasNoFallback(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "vmmd")
	if _, err := resolveGuestDiskHelper(exe); err == nil || !strings.Contains(err.Error(), "vmmd-guestdisk") {
		t.Fatalf("missing helper: err = %v", err)
	}
	helper := filepath.Join(dir, "vmmd-guestdisk")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveGuestDiskHelper(exe); err == nil {
		t.Fatal("non-executable helper accepted")
	}
	if err := os.Chmod(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveGuestDiskHelper(exe); err != nil || got != helper {
		t.Fatalf("resolveGuestDiskHelper = %q, %v", got, err)
	}
}

func TestJailDriveOwner(t *testing.T) {
	for _, tc := range []struct {
		uid, gid int
		ok       bool
	}{{JailUIDBase, JailUIDBase, true}, {JailUIDMax, JailUIDMax, true}, {0, 0, false}, {JailUIDBase - 1, JailUIDBase - 1, false}, {JailUIDMax + 1, JailUIDMax + 1, false}, {JailUIDBase, JailUIDBase + 1, false}} {
		if got := jailDriveOwner(tc.uid, tc.gid); got != tc.ok {
			t.Errorf("jailDriveOwner(%d, %d) = %v", tc.uid, tc.gid, got)
		}
	}
}

func TestFullRootfsMarkerLayout(t *testing.T) {
	marker := filepath.FromSlash(strings.TrimPrefix(fullRootfsMarkerImagePath, "/"))
	for _, tc := range []struct {
		name    string
		setup   func(root string)
		full    bool
		wantErr string
	}{
		{name: "missing", setup: func(string) {}},
		{name: "valid", full: true, setup: func(root string) {
			mustWriteFile(t, filepath.Join(root, marker), []byte(api.FullRootfsMarkerValue))
		}},
		{name: "bad payload", wantErr: "invalid full-rootfs marker payload", setup: func(root string) {
			mustWriteFile(t, filepath.Join(root, marker), []byte("nope"))
		}},
		{name: "symlinked marker", wantErr: "not a regular file", setup: func(root string) {
			mustMkdir(t, filepath.Dir(filepath.Join(root, marker)))
			if err := os.Symlink("/etc/passwd", filepath.Join(root, marker)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked parent", wantErr: "not a real directory", setup: func(root string) {
			if err := os.Symlink("/etc", filepath.Join(root, "etc")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.setup(root)
			layout, err := inspectDrive(context.Background(), &dirDriveImage{root: root}, []string{secretsEnvPath}, nil)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || layout.fullRootfs != tc.full {
				t.Fatalf("layout = %+v, %v; want full=%v", layout.fullRootfs, err, tc.full)
			}
		})
	}
	if got := driveImagePath(false, secretsEnvPath); got != "/upper/etc/faas/secrets.env" {
		t.Errorf("optimized path = %s", got)
	}
	if got := driveImagePath(true, secretsEnvPath); got != "/etc/faas/secrets.env" {
		t.Errorf("full-rootfs path = %s", got)
	}
}

func mustWriteFile(t *testing.T, p string, data []byte) {
	t.Helper()
	mustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestEditDriveOpensReplaysAndCloses(t *testing.T) {
	v := newStagingVMM(t, "inst-edit")
	drive := filepath.Join(v.chrootRoot("inst-edit"), layerImageName)
	img := &dirDriveImage{root: t.TempDir()}
	prev := openDriveImage
	openDriveImage = func(_ *JailerVMM, got string) (driveImage, error) {
		if got != drive {
			t.Fatalf("opened %s, want %s", got, drive)
		}
		return img, nil
	}
	t.Cleanup(func() { openDriveImage = prev })
	called := false
	if err := v.editDrive(context.Background(), nativeLaunchRecord{}, "inst-edit", drive, func(driveImage) error {
		if img.prepared != 1 {
			t.Fatal("editor ran before the journal replay")
		}
		called = true
		return nil
	}, nil); err != nil || !called {
		t.Fatalf("editDrive = %v called=%v", err, called)
	}
}

// No VM drive is ever mounted on the host (ADR-20261025090100): a new call
// site would hand guest-written ext4 metadata to the host kernel. Bind and
// tmpfs mounts stay legal; ext4, loop and loop-device attachment do not.
func TestNothingInFcvmMountsADriveOnTheHost(t *testing.T) {
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`exec\.Command(Context)?\([^)]*"mount"[^)]*(loop|ext4)`),
		regexp.MustCompile(`(unix|syscall)\.Mount\([^)]*"ext[234]"`),
		regexp.MustCompile(`IoctlLoopConfigure|LOOP_CONFIGURE|LOOP_SET_FD|LOOP_CTL_GET_FREE`),
	}
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range forbidden {
			if loc := re.FindIndex(body); loc != nil {
				t.Errorf("%s mounts or attaches an image on the host (%q); read and edit drives through guestdisk", name, body[loc[0]:loc[1]])
			}
		}
	}
}
