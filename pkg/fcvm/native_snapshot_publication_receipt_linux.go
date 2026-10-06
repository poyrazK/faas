//go:build linux

// adr: 568 — acknowledged object receipts survive volatile journal loss.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

func nativePublicationReceiptName(capture, kind string) string {
	return capture + "." + kind + ".receipt.json"
}

func nativePublicationReceiptEntry(name string) (capture, kind string, ok bool) {
	base, ok := strings.CutSuffix(name, ".receipt.json")
	if !ok {
		return "", "", false
	}
	capture, kind, ok = strings.Cut(base, ".")
	if !ok || !canonicalNativeHelperID(capture) {
		return "", "", false
	}
	switch kind {
	case "mem", "vmstate", "drive", "backing":
		return capture, kind, true
	}
	return "", "", false
}

func (j *linuxNativeSnapshotPublicationJournal) requireIntentLocked(ctx context.Context, expected nativeSnapshotPublicationIntent) error {
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

func (j *linuxNativeSnapshotPublicationJournal) readObjectLocked(ctx context.Context, intent nativeSnapshotPublicationIntent, kind string) (r nativeSnapshotPublicationObjectReceipt, err error) {
	if nativePublicationObjectKey(intent, kind) == "" {
		return r, errors.New("native snapshot publication: unsupported object kind")
	}
	if err := j.requireIntentLocked(ctx, intent); err != nil {
		return r, err
	}
	fd, err := unix.Openat2(int(j.owner.Fd()), nativePublicationReceiptName(intent.Capture.CaptureID, kind), &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV})
	if err != nil {
		return r, err
	}
	file := os.NewFile(uintptr(fd), "native-original-object-receipt")
	defer func() { err = errors.Join(err, file.Close()) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return r, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o077 != 0 || stat.Nlink != 1 || stat.Size <= 0 || stat.Size > api.NativeSnapshotPublicationRecordMaxBytes {
		return r, errors.New("native snapshot publication: one bounded private object receipt is required")
	}
	d := json.NewDecoder(io.LimitReader(file, api.NativeSnapshotPublicationRecordMaxBytes+1))
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return r, errors.New("native snapshot publication: trailing object receipt data")
	}
	if r.Kind != kind || r.File != (nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) {
		return r, errors.New("native snapshot publication: original receipt file changed")
	}
	return r, errors.Join(r.validate(intent), ctx.Err())
}

func (j *linuxNativeSnapshotPublicationJournal) RecordObject(ctx context.Context, intent nativeSnapshotPublicationIntent, kind string, object storage.ExclusiveArtifactReceipt) (r nativeSnapshotPublicationObjectReceipt, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	defer func() {
		if err != nil {
			r = nativeSnapshotPublicationObjectReceipt{}
		}
	}()
	if err := j.requireIntentLocked(ctx, intent); err != nil {
		return r, err
	}
	fd, err := unix.Openat(int(j.owner.Fd()), ".", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return r, err
	}
	file := os.NewFile(uintptr(fd), "native-original-object-receipt")
	defer func() { err = errors.Join(err, file.Close()) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return r, err
	}
	r = nativeSnapshotPublicationObjectReceipt{Version: 1, Directory: j.identity, File: nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, IntentFile: intent.File, CaptureID: intent.Capture.CaptureID, Kind: kind, Object: object}
	if err := r.validate(intent); err != nil {
		return r, err
	}
	body, err := json.Marshal(r)
	if err != nil || len(body) > api.NativeSnapshotPublicationRecordMaxBytes {
		return r, errors.Join(err, errors.New("native snapshot publication: invalid bounded object receipt"))
	}
	if _, err := file.Write(body); err != nil {
		return r, err
	}
	if err := errors.Join(file.Sync(), j.requireIntentLocked(ctx, intent)); err != nil {
		return r, err
	}
	if err := unix.Linkat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), int(j.owner.Fd()), nativePublicationReceiptName(r.CaptureID, r.Kind), unix.AT_SYMLINK_FOLLOW); err != nil {
		if errors.Is(err, unix.EEXIST) {
			err = errors.Join(storage.ErrArtifactExists, err)
		}
		return r, err
	}
	// An uncertain journal acknowledgement cannot be repaired by adopting the
	// now-visible file. The successful original caller alone receives this record.
	return r, errors.Join(j.owner.Sync(), j.requireIntentLocked(ctx, intent), ctx.Err())
}

func (j *linuxNativeSnapshotPublicationJournal) RequireObject(ctx context.Context, intent nativeSnapshotPublicationIntent, expected nativeSnapshotPublicationObjectReceipt) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	current, err := j.readObjectLocked(ctx, intent, expected.Kind)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, expected) {
		return errors.New("native snapshot publication: original durable object receipt changed")
	}
	return ctx.Err()
}
