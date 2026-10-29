// adr: 20261025090100
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/guestdisk"
)

// A VM drive is guest-written: the tenant is root in its microVM and can
// write any block of its writable drive, and the customer image seeds it. The
// host kernel therefore never mounts one (ADR-20261025090100). vmmd reads and
// edits drives with e2fsprogs in the vmmd-guestdisk sandbox instead, running
// as the drive owner's jail uid.

// preBootStageTimings break the pre-boot staging interval down for the
// operator-only restore timing record. They are not fields on the customer
// wake.restore_breakdown event.
type preBootStageTimings struct {
	Replay       time.Duration // opening the drive and replaying its journal
	Prepare      time.Duration // validation, payload construction and digest
	Check        time.Duration // inspecting the drive's layout and existing files
	Write        time.Duration // planning, writing and reading back every file
	FilesTotal   int
	FilesWritten int // files the session wrote and verified
}

// driveImage is one drive opened for a staging session.
type driveImage interface {
	// Prepare replays the drive's journal when it needs recovery.
	Prepare(ctx context.Context) (bool, error)
	Inspect(ctx context.Context, paths []string, reads map[string]guestdisk.Read) (map[string]guestdisk.Entry, error)
	WriteFilesAfter(ctx context.Context, files []guestdisk.File, inspected map[string]guestdisk.Entry) error
	RemoveAll(ctx context.Context, paths []string) error
	List(ctx context.Context, dirs []string, maxEntries int) (map[string][]guestdisk.DirEntry, error)
	DumpFiles(ctx context.Context, dir, dest string, b guestdisk.DumpBudget) (string, error)
	Close() error
}

var _ driveImage = (*guestdisk.Session)(nil)

// openDriveImage opens drive for one session. A package variable so the
// pure-Go test tier can substitute a directory for the image.
var openDriveImage = func(v *JailerVMM, drive string) (driveImage, error) {
	ed, err := v.guestDiskEditor()
	if err != nil {
		return nil, err
	}
	return ed.Open(drive)
}

// guestDiskHelperOverride replaces the vmmd-guestdisk path in tests whose
// binary dispatches the helper itself (TestMain).
var guestDiskHelperOverride string

type guestDiskState struct {
	mu     sync.Mutex
	editor *guestdisk.Editor
}

func (v *JailerVMM) guestDiskEditor() (*guestdisk.Editor, error) {
	v.guestDisk.mu.Lock()
	defer v.guestDisk.mu.Unlock()
	if v.guestDisk.editor != nil {
		return v.guestDisk.editor, nil
	}
	helper := guestDiskHelperOverride
	if helper == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("vmm: locate guest disk helper: %w", err)
		}
		if helper, err = resolveGuestDiskHelper(exe); err != nil {
			return nil, err
		}
	}
	ed, err := guestdisk.New(guestdisk.Config{
		Helper:      helper,
		ScratchRoot: filepath.Join(v.chrootBase, ".guestdisk"),
		AllowOwner:  jailDriveOwner,
	})
	if err != nil {
		return nil, fmt.Errorf("vmm: guest disk editor: %w", err)
	}
	v.guestDisk.editor = ed
	return ed, nil
}

// resolveGuestDiskHelper finds vmmd-guestdisk beside this exact vmmd. There
// is no fallback: without the sandbox, vmmd refuses to touch guest drives
// rather than parse one as root.
func resolveGuestDiskHelper(executable string) (string, error) {
	helper := filepath.Join(filepath.Dir(executable), "vmmd-guestdisk")
	info, err := os.Stat(helper)
	if err != nil {
		return "", fmt.Errorf("vmm: guest disk helper %s: %w (it ships beside vmmd in every release)", helper, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("vmm: guest disk helper %s must be an executable regular file", helper)
	}
	return helper, nil
}

// jailDriveOwner admits the identities vmmd chowns drives to: one jail uid
// with its matching gid (chownJail).
func jailDriveOwner(uid, gid int) bool {
	return uid >= JailUIDBase && uid <= JailUIDMax && gid == uid
}

// editDrive opens drive in the sandbox, replays its journal and runs fn. In
// native mode the edit runs under the prepared owner's staging authority and
// only on the canonical writable drive. replay, when set, receives the time
// spent opening the drive and replaying its journal.
func (v *JailerVMM) editDrive(ctx context.Context, owner nativeLaunchRecord, instance, drive string, fn func(driveImage) error, replay *time.Duration) error {
	run := func() (err error) {
		started := time.Now()
		img, err := openDriveImage(v, drive)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, img.Close()) }()
		if _, err := img.Prepare(ctx); err != nil {
			return fmt.Errorf("replay drive journal: %w", err)
		}
		if replay != nil {
			*replay = time.Since(started)
		}
		return fn(img)
	}
	if v.nativeRecovery == nil {
		return run()
	}
	if owner.Lease.Instance != instance || drive != filepath.Join(v.chrootRoot(instance), layerImageName) {
		return errors.New("native drive edit: staging requires the original canonical writable drive")
	}
	j := nativeLoopMountJournal{owner: v.nativeRecovery.journal, backend: v.nativeRecovery.loopMounts, helperGroups: v.nativeRecovery.helperGroups}
	return j.editSession(ctx, owner, run)
}

// driveFile is one file staging leaves on drive1. path is in the optimized
// layout ("upper/etc/faas/..."); a full-rootfs drive drops the "upper/".
type driveFile struct {
	what string
	path string
	want []byte
	mode os.FileMode
	// keepAttrs keeps an existing regular file's owner and mode, as the
	// in-place rewrite of the mounted-drive writers always did.
	keepAttrs bool
	// same, when set, decides whether a file already on the drive is as
	// good as writing want; otherwise it must equal want byte for byte.
	same func(got []byte) bool
}

// driveImagePath maps an optimized-layout path onto the drive. The optimized
// two-drive artifact stores mutable runtime files below /upper; full-rootfs
// artifacts are already mounted as the guest's root and therefore receive the
// same files directly under /. The marker is written only by the platform
// builder, so a malformed value fails closed instead of silently writing
// state to a path guest-init will never read.
func driveImagePath(fullRootfs bool, optimized string) string {
	if fullRootfs {
		return "/" + strings.TrimPrefix(optimized, "upper/")
	}
	return "/" + optimized
}

func imageComponents(p string) []string {
	var out []string
	for i := 1; i < len(p); i++ {
		if p[i] == '/' {
			out = append(out, p[:i])
		}
	}
	return append(out, p)
}

var fullRootfsMarkerImagePath = "/" + strings.TrimPrefix(api.FullRootfsMarkerPath, "/")

// driveLayout is one inspection of a drive: its layout and what every path a
// staging session may touch currently names.
type driveLayout struct {
	fullRootfs bool
	entries    map[string]guestdisk.Entry
}

// inspectDrive reads the full-rootfs marker and, in the same sandboxed run,
// every component of paths under both layouts. reads asks for contents of
// those paths (by optimized path) up to the given bounds.
func inspectDrive(ctx context.Context, img driveImage, paths []string, reads map[string]int64) (driveLayout, error) {
	probe := imageComponents(fullRootfsMarkerImagePath)
	want := map[string]guestdisk.Read{fullRootfsMarkerImagePath: {Max: int64(len(api.FullRootfsMarkerValue))}}
	for _, p := range paths {
		for _, full := range []bool{false, true} {
			ip := driveImagePath(full, p)
			probe = append(probe, imageComponents(ip)...)
			if max, ok := reads[p]; ok {
				want[ip] = guestdisk.Read{Max: max}
			}
		}
	}
	entries, err := img.Inspect(ctx, probe, want)
	if err != nil {
		return driveLayout{}, err
	}
	full, err := fullRootfsMarker(entries)
	if err != nil {
		return driveLayout{}, err
	}
	return driveLayout{fullRootfs: full, entries: entries}, nil
}

// fullRootfsMarker decides the drive's layout: a missing marker (or a missing
// parent) means the optimized upper/ layout, a regular file with the exact
// payload means full rootfs, and anything else fails closed, including a
// parent that is a symlink, so a planted link can never move the files away
// from where guest-init reads them.
func fullRootfsMarker(entries map[string]guestdisk.Entry) (bool, error) {
	comps := imageComponents(fullRootfsMarkerImagePath)
	for _, dir := range comps[:len(comps)-1] {
		switch e := entries[dir]; e.Kind {
		case guestdisk.Directory:
		case guestdisk.Missing:
			return false, nil
		default:
			return false, fmt.Errorf("full-rootfs marker parent %s is a %s, not a real directory", dir, e.Kind)
		}
	}
	switch e := entries[fullRootfsMarkerImagePath]; e.Kind {
	case guestdisk.Missing:
		return false, nil
	case guestdisk.Regular:
		if e.Size != int64(len(api.FullRootfsMarkerValue)) || string(e.Data) != api.FullRootfsMarkerValue {
			return false, errors.New("invalid full-rootfs marker payload")
		}
		return true, nil
	case guestdisk.Unknown:
		return false, errors.New("inspect full-rootfs marker: entry is unreadable")
	default:
		return false, errors.New("full-rootfs marker is not a regular file")
	}
}

// stageDriveFiles writes files onto an inspected drive in one sandboxed run.
// A failure attributed to one file is wrapped with that file's operation
// label, so operator triage strings name the staged file.
func stageDriveFiles(ctx context.Context, img driveImage, layout driveLayout, files []driveFile) error {
	out := make([]guestdisk.File, len(files))
	byPath := make(map[string]string, len(files))
	for i, f := range files {
		p := driveImagePath(layout.fullRootfs, f.path)
		out[i] = guestdisk.File{Path: p, Data: f.want, Mode: uint32(f.mode.Perm()), KeepAttrs: f.keepAttrs}
		byPath[p] = f.what
	}
	err := img.WriteFilesAfter(ctx, out, layout.entries)
	var pathErr *guestdisk.PathError
	if errors.As(err, &pathErr) {
		if what, ok := byPath[pathErr.Path]; ok {
			return fmt.Errorf("%s: %w", what, err)
		}
	}
	return err
}

// stageDriveFile is the single-file session the public Stage* methods use.
func (v *JailerVMM) stageDriveFile(ctx context.Context, owner nativeLaunchRecord, instance, drive string, f driveFile) error {
	return v.editDrive(ctx, owner, instance, drive, func(img driveImage) error {
		layout, err := inspectDrive(ctx, img, []string{f.path}, nil)
		if err != nil {
			return fmt.Errorf("%s: %w", f.what, err)
		}
		return stageDriveFiles(ctx, img, layout, []driveFile{f})
	}, nil)
}

// driveFilesPresent reports whether every file is already on the drive
// exactly as staging would leave it: each parent a real directory, the file
// regular, root-owned, with the file's permission bits and byte-identical
// contents (or contents its same func accepts). Only then is skipping the
// writes indistinguishable from doing them.
func driveFilesPresent(layout driveLayout, files []driveFile) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if f.path == "" {
			return false
		}
		comps := imageComponents(driveImagePath(layout.fullRootfs, f.path))
		for _, dir := range comps[:len(comps)-1] {
			if layout.entries[dir].Kind != guestdisk.Directory {
				return false
			}
		}
		e := layout.entries[comps[len(comps)-1]]
		if e.Kind != guestdisk.Regular || e.Perm() != f.mode.Perm() || e.UID != 0 || e.Data == nil {
			return false
		}
		if f.same != nil {
			if !f.same(e.Data) {
				return false
			}
		} else if string(e.Data) != string(f.want) {
			return false
		}
	}
	return true
}

// presentReads bounds the contents a presence check reads per file.
func presentReads(files []driveFile) map[string]int64 {
	reads := make(map[string]int64, len(files))
	for _, f := range files {
		if f.same != nil {
			reads[f.path] = preBootSameMaxBytes
		} else {
			reads[f.path] = int64(len(f.want))
		}
	}
	return reads
}
