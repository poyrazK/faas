//go:build linux

// adr: 568 — verified restore inputs remain private anonymous disk descriptors.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

type nativeSnapshotRestoreInputs struct {
	Cohort  nativeSnapshotRestoreCohort
	Files   [4]*os.File
	Backing BackingIdentity
}

// The synchronous consumer owns no descriptor beyond this call. This barrier
// supplies verified bytes, never permission to allocate, launch, smoke, restore
// or activate. Those operations need a separately fenced original execution.
// Neither storage.Get, LocalPath, cache fallback nor a host-supplied path is used.
func withNativeSnapshotRestoreInputs(ctx context.Context, journal nativeSnapshotRestoreReceiptJournal, backend storage.StorageBackend, completed nativeQualificationCaptureRecord, directory string, consume func(nativeSnapshotRestoreInputs) error) (result error) {
	if journal == nil || backend == nil || consume == nil {
		return errors.New("native snapshot restore: original receipt adapters and consumer are required")
	}
	cohort, err := journal.ReadRestoreCohort(ctx, completed.CaptureID)
	if err != nil {
		return err
	}
	if err := cohort.validate(completed); err != nil {
		return err
	}
	if cohort.Objects[3].Object.LogicalBytes > api.NativeSnapshotBackingRecordMaxBytes {
		return errors.New("native snapshot restore: original backing sidecar exceeds its parser bound")
	}
	identity, err := nativeDiskImageRootIdentity(directory)
	if err != nil {
		return err
	}
	if nativePublicationRootsOverlap(directory, cohort.Intent.JailBase) {
		return errors.New("native snapshot restore: materialization requires a persistent disk outside the jail")
	}
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	root := os.NewFile(uintptr(fd), "native-restore-input-root")
	defer func() { result = errors.Join(result, root.Close(), ctx.Err()) }()
	if err := requireNativeRestoreInputRoot(directory, root, identity); err != nil {
		return err
	}
	inputs := nativeSnapshotRestoreInputs{Cohort: cohort}
	defer func() {
		for _, file := range inputs.Files {
			if file != nil {
				result = errors.Join(result, file.Close())
			}
		}
	}()
	for i, receipt := range cohort.Objects {
		if err := errors.Join(cohort.require(ctx, journal), requireNativeRestoreInputRoot(directory, root, identity)); err != nil {
			return err
		}
		file, err := materializeNativeRestoreInput(ctx, root, backend, receipt.Object)
		if err != nil {
			return err
		}
		inputs.Files[i] = file
	}
	backing, err := readNativeRestoreBacking(inputs.Files[3])
	if err != nil || backing != completed.Backing {
		return errors.Join(err, errors.New("native snapshot restore: backing sidecar differs from original completion"))
	}
	inputs.Backing = backing
	if err := errors.Join(cohort.require(ctx, journal), requireNativeRestoreInputRoot(directory, root, identity)); err != nil {
		return err
	}
	return errors.Join(consume(inputs), cohort.require(ctx, journal), ctx.Err())
}

func requireNativeRestoreInputRoot(directory string, file *os.File, expected nativeLoopIdentity) error {
	current, err := nativeDiskImageRootIdentity(directory)
	var stat unix.Stat_t
	if err := errors.Join(err, unix.Fstat(int(file.Fd()), &stat)); err != nil {
		return err
	}
	if current != expected || expected != (nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("native snapshot restore: original private disk root changed")
	}
	return nil
}

func materializeNativeRestoreInput(ctx context.Context, root *os.File, backend storage.StorageBackend, receipt storage.ExclusiveArtifactReceipt) (input *os.File, result error) {
	fd, err := unix.Openat(int(root.Fd()), ".", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	output := os.NewFile(uintptr(fd), "native-unverified-restore-input")
	defer func() {
		result = errors.Join(result, output.Close())
		if result != nil && input != nil {
			result = errors.Join(result, input.Close())
			input = nil
		}
	}()
	if _, err := storage.CopyExclusiveArtifact(ctx, backend, receipt, output); err != nil {
		return nil, err
	}
	if err := errors.Join(output.Chmod(0o400), output.Sync(), ctx.Err()); err != nil {
		return nil, err
	}
	readFD, err := unix.Open(nativeImageFDPath(output), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	input = os.NewFile(uintptr(readFD), "/proc/self/fd/"+strconv.Itoa(readFD))
	var original, sealed unix.Stat_t
	if err := errors.Join(unix.Fstat(fd, &original), unix.Fstat(readFD, &sealed)); err != nil {
		return input, err
	}
	if original.Dev != sealed.Dev || original.Ino != sealed.Ino || sealed.Mode&unix.S_IFMT != unix.S_IFREG || sealed.Mode&0o7777 != 0o400 || sealed.Uid != uint32(os.Geteuid()) || sealed.Nlink != 0 || sealed.Size != receipt.LogicalBytes {
		return input, errors.New("native snapshot restore: sealed descriptor lost its original anonymous identity")
	}
	return input, ctx.Err()
}

func readNativeRestoreBacking(file *os.File) (backing BackingIdentity, result error) {
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > api.NativeSnapshotBackingRecordMaxBytes {
		return backing, errors.Join(err, ErrSnapshotBackingUnverified)
	}
	body := make([]byte, info.Size())
	if _, err := file.ReadAt(body, 0); err != nil {
		return backing, err
	}
	if _, err := nativeJournalObjectFields(body, []string{"version", "kernel", "base", "timer"}); err != nil {
		return backing, err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&backing); err != nil {
		return backing, err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) || !backing.complete() {
		return BackingIdentity{}, ErrSnapshotBackingUnverified
	}
	return backing, nil
}
