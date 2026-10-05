//go:build linux

// adr: 568 — anonymous immutable intents survive volatile journal loss.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

type linuxNativeSnapshotPublicationJournal struct {
	root, base, images string
	mu                 sync.Mutex
	owner              *os.File
	identity           nativeLoopIdentity
}

func newNativeSnapshotPublicationJournal(root, base, images string) nativeSnapshotPublicationJournal {
	if root == "" {
		return nil
	}
	return &linuxNativeSnapshotPublicationJournal{root: root, base: base, images: images}
}

func nativePublicationRootsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(os.PathSeparator)) || strings.HasPrefix(b, a+string(os.PathSeparator))
}

func (j *linuxNativeSnapshotPublicationJournal) Acquire(ctx context.Context) (err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.owner != nil {
		return errors.Join(j.checkLocked(), ctx.Err())
	}
	if !filepath.IsAbs(j.base) || filepath.Clean(j.base) != j.base || j.base == "/" || nativePublicationRootsOverlap(j.root, j.base) || j.images != "" && nativePublicationRootsOverlap(j.root, j.images) {
		return errors.New("native snapshot publication: a separate persistent root outside jail and image staging is required")
	}
	identity, err := nativeDiskImageRootIdentity(j.root)
	if err != nil {
		return err
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, j.root, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return err
	}
	owner := os.NewFile(uintptr(fd), "native-capture-publication-root")
	if err := lockNativePublicationDirectory(ctx, owner); err != nil {
		return errors.Join(err, owner.Close())
	}
	j.owner, j.identity = owner, identity
	if err := errors.Join(j.checkLocked(), j.inventoryLocked(ctx)); err != nil {
		j.owner = nil
		return errors.Join(err, owner.Close())
	}
	return nil
}

func lockNativePublicationDirectory(ctx context.Context, owner *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := unix.Flock(int(owner.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (j *linuxNativeSnapshotPublicationJournal) Check() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.checkLocked()
}

func (j *linuxNativeSnapshotPublicationJournal) checkLocked() error {
	if j.owner == nil {
		return errors.New("native snapshot publication: original directory owner is unavailable")
	}
	current, err := nativeDiskImageRootIdentity(j.root)
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(j.owner.Fd()), &stat); err != nil {
		return err
	}
	if current != j.identity || uint64(stat.Dev) != j.identity.Device || stat.Ino != j.identity.Inode || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("native snapshot publication: original private directory changed")
	}
	return nil
}

func (j *linuxNativeSnapshotPublicationJournal) validateIntent(r nativeSnapshotPublicationIntent) error {
	if err := r.validate(); err != nil {
		return err
	}
	if r.Directory != j.identity || r.JailBase != j.base {
		return errors.New("native snapshot publication: intent belongs to another directory or jail")
	}
	return nil
}

func (j *linuxNativeSnapshotPublicationJournal) readLocked(ctx context.Context, capture string) (r nativeSnapshotPublicationIntent, err error) {
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if !canonicalNativeHelperID(capture) {
		return r, errors.New("native snapshot publication: original canonical capture is required")
	}
	fd, err := unix.Openat2(int(j.owner.Fd()), capture+".json", &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV})
	if err != nil {
		return r, err
	}
	file := os.NewFile(uintptr(fd), "native-capture-publication-intent")
	defer func() { err = errors.Join(err, file.Close()) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return r, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Size <= 0 || stat.Size > 2<<20 {
		return r, errors.New("native snapshot publication: intent must be one bounded private regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 2<<20+1))
	if err := decoder.Decode(&r); err != nil {
		return r, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return r, errors.New("native snapshot publication: trailing intent data")
	}
	if r.Capture.CaptureID != capture {
		return r, errors.New("native snapshot publication: filename changed original capture")
	}
	if r.File != (nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) {
		return r, errors.New("native snapshot publication: original intent inode was substituted")
	}
	return r, errors.Join(j.validateIntent(r), ctx.Err())
}

// Inventory only validates. It never removes an intent or manufactures a live
// permit, even when the original boot's volatile journal is entirely absent.
func (j *linuxNativeSnapshotPublicationJournal) inventoryLocked(ctx context.Context) (err error) {
	fd, err := unix.Openat(int(j.owner.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(fd), "native-capture-publication-inventory")
	entries, readErr := directory.ReadDir(-1)
	if err := errors.Join(readErr, directory.Close()); err != nil {
		return err
	}
	for _, entry := range entries {
		capture, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !canonicalNativeHelperID(capture) || !entry.Type().IsRegular() {
			return errors.New("native snapshot publication: unowned entry requires quarantine")
		}
		if _, err := j.readLocked(ctx, capture); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (j *linuxNativeSnapshotPublicationJournal) Begin(ctx context.Context, intent nativeSnapshotPublicationIntent) (r nativeSnapshotPublicationIntent, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.checkLocked(); err != nil {
		return r, err
	}
	intent.Directory = j.identity
	if err := j.inventoryLocked(ctx); err != nil {
		return r, err
	}
	fd, err := unix.Openat(int(j.owner.Fd()), ".", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return r, err
	}
	file := os.NewFile(uintptr(fd), "native-capture-publication-intent")
	defer func() { err = errors.Join(err, file.Close()) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return r, err
	}
	intent.File = nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}
	if err := j.validateIntent(intent); err != nil {
		return r, err
	}
	data, err := json.Marshal(intent)
	if err != nil || len(data) > 2<<20 {
		return r, errors.Join(err, errors.New("native snapshot publication: invalid bounded intent"))
	}
	if _, err := file.Write(data); err != nil {
		return r, err
	}
	if err := errors.Join(file.Sync(), ctx.Err(), j.checkLocked()); err != nil {
		return r, err
	}
	if err := unix.Linkat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), int(j.owner.Fd()), intent.Capture.CaptureID+".json", unix.AT_SYMLINK_FOLLOW); err != nil {
		if errors.Is(err, unix.EEXIST) {
			err = errors.Join(storage.ErrArtifactExists, err)
		}
		return r, err
	}
	// Post-link errors preserve the original uncertain intent. Begin cannot be
	// replayed, and only the successful caller receives an in-memory permit.
	if err := errors.Join(j.owner.Sync(), ctx.Err()); err != nil {
		return r, err
	}
	return intent, nil
}

func (j *linuxNativeSnapshotPublicationJournal) Require(ctx context.Context, expected nativeSnapshotPublicationIntent) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.checkLocked(); err != nil {
		return err
	}
	current, err := j.readLocked(ctx, expected.Capture.CaptureID)
	if err != nil {
		return err
	}
	if current != expected {
		return errors.New("native snapshot publication: original durable intent changed")
	}
	return nil
}
