//go:build linux

// adr: 568 — artifact retirement is bound to a completed original capture.
package fcvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

type nativeSnapshotPublicationRetirement struct {
	Version       int                `json:"version"`
	Directory     nativeLoopIdentity `json:"directory"`
	File          nativeLoopIdentity `json:"file"`
	IntentFile    nativeLoopIdentity `json:"intent_file"`
	CaptureID     string             `json:"capture_id"`
	ObjectsSHA256 string             `json:"objects_sha256"`
}

func (r *nativeSnapshotPublicationRetirement) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, []string{"version", "directory", "file", "intent_file", "capture_id", "objects_sha256"})
	if err != nil {
		return err
	}
	for _, name := range []string{"directory", "file", "intent_file"} {
		if _, err := nativeJournalObjectFields(fields[name], []string{"device", "inode"}); err != nil {
			return err
		}
	}
	type plain nativeSnapshotPublicationRetirement
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode((*plain)(r))
}

func nativePublicationRetirementName(capture string, complete bool) string {
	if complete {
		return capture + ".retired.json"
	}
	return capture + ".retiring.json"
}

func nativePublicationRetirementEntry(name string) (capture string, complete, ok bool) {
	if capture, ok = strings.CutSuffix(name, ".retired.json"); ok && canonicalNativeHelperID(capture) {
		return capture, true, true
	}
	capture, ok = strings.CutSuffix(name, ".retiring.json")
	return capture, false, ok && canonicalNativeHelperID(capture)
}

func nativePublicationObjectsDigest(objects [4]nativeSnapshotPublicationObjectReceipt) (string, error) {
	encoded, err := json.Marshal(objects)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (r nativeSnapshotPublicationRetirement) validate(intent nativeSnapshotPublicationIntent,
	objects [4]nativeSnapshotPublicationObjectReceipt) error {
	digest, err := nativePublicationObjectsDigest(objects)
	if err != nil {
		return err
	}
	if r.Version != 1 || r.Directory != intent.Directory || r.File.Device != r.Directory.Device || r.File.Inode == 0 ||
		r.File == r.Directory || r.File == intent.File || r.IntentFile != intent.File || r.CaptureID != intent.Capture.CaptureID ||
		r.ObjectsSHA256 != digest || len(r.ObjectsSHA256) != sha256.Size*2 {
		return errors.New("native snapshot publication: retirement differs from the original object cohort")
	}
	if decoded, err := hex.DecodeString(r.ObjectsSHA256); err != nil || hex.EncodeToString(decoded) != r.ObjectsSHA256 {
		return errors.New("native snapshot publication: retirement digest is not canonical")
	}
	return nil
}

func (j *linuxNativeSnapshotPublicationJournal) readRetirementStateLocked(ctx context.Context, intent nativeSnapshotPublicationIntent,
	objects [4]nativeSnapshotPublicationObjectReceipt, complete bool) (record nativeSnapshotPublicationRetirement, exists bool, err error) {
	if err := j.checkLocked(); err != nil {
		return record, false, err
	}
	fd, err := unix.Openat2(int(j.owner.Fd()), nativePublicationRetirementName(intent.Capture.CaptureID, complete), &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	if errors.Is(err, unix.ENOENT) {
		return record, false, ctx.Err()
	}
	if err != nil {
		return record, false, err
	}
	file := os.NewFile(uintptr(fd), "native-capture-retirement")
	defer func() { err = errors.Join(err, file.Close()) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return record, false, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o077 != 0 || stat.Nlink != 1 ||
		stat.Size <= 0 || stat.Size > api.NativeSnapshotPublicationRecordMaxBytes {
		return record, false, errors.New("native snapshot publication: retirement must be one bounded private regular file")
	}
	d := json.NewDecoder(io.LimitReader(file, api.NativeSnapshotPublicationRecordMaxBytes+1))
	if err := d.Decode(&record); err != nil {
		return record, false, err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record, false, errors.New("native snapshot publication: trailing retirement data")
	}
	if record.File != (nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) {
		return record, false, errors.New("native snapshot publication: retirement file identity changed")
	}
	return record, true, errors.Join(record.validate(intent, objects), ctx.Err())
}

func (j *linuxNativeSnapshotPublicationJournal) readRetirementLocked(ctx context.Context, intent nativeSnapshotPublicationIntent,
	objects [4]nativeSnapshotPublicationObjectReceipt) (nativeSnapshotPublicationRetirement, bool, error) {
	return j.readRetirementStateLocked(ctx, intent, objects, true)
}

// RetireRestoreCohort conditionally removes the exact four original objects,
// then persists a tombstone which permanently prevents that capture from being
// restored again. Repeating the operation is safe after partial deletion or a
// lost acknowledgement because each backend delete is receipt-conditional.
func (j *linuxNativeSnapshotPublicationJournal) RetireRestoreCohort(ctx context.Context, capture string, backend storage.StorageBackend) error {
	if backend == nil || !canonicalNativeHelperID(capture) {
		return state.ErrInvalidArgument
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.checkLocked(); err != nil {
		return err
	}
	intent, err := j.readLocked(ctx, capture)
	if err != nil {
		return err
	}
	var objects [4]nativeSnapshotPublicationObjectReceipt
	for i, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		objects[i], err = j.readObjectLocked(ctx, intent, kind)
		if err != nil {
			return err
		}
	}
	if _, complete, err := j.readRetirementLocked(ctx, intent, objects); err != nil {
		return err
	} else if complete {
		if _, pending, err := j.readRetirementStateLocked(ctx, intent, objects, false); err != nil || !pending {
			return errors.Join(err, state.ErrConflict)
		}
		return ctx.Err()
	}
	_, pending, err := j.readRetirementStateLocked(ctx, intent, objects, false)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if err := storage.CheckExclusiveArtifactRetirement(ctx, backend, object.Object); err != nil {
			return err
		}
	}
	if !pending {
		if err := j.writeRetirementStateLocked(ctx, intent, objects, false); err != nil {
			return err
		}
	}
	for _, object := range objects {
		if err := storage.RetireExclusiveArtifact(ctx, backend, object.Object); err != nil {
			return err
		}
	}
	if err := j.requireIntentLocked(ctx, intent); err != nil {
		return err
	}
	for i, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		current, err := j.readObjectLocked(ctx, intent, kind)
		if err != nil || !reflect.DeepEqual(current, objects[i]) {
			return errors.Join(err, state.ErrConflict)
		}
	}
	return j.writeRetirementStateLocked(ctx, intent, objects, true)
}

// RecoverPendingRetirements discovers only durable in-progress records. Such
// a record is written after the scheduler has authorized retirement and before
// the first conditional object delete, so it is sufficient to retry the same
// original receipt-bound operation after a daemon restart.
func (j *linuxNativeSnapshotPublicationJournal) RecoverPendingRetirements(ctx context.Context, backend storage.StorageBackend) error {
	captures, err := j.pendingRetirementCaptures(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, capture := range captures {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		if err := j.RetireRestoreCohort(ctx, capture, backend); err != nil {
			result = errors.Join(result, fmt.Errorf("native snapshot publication: recover pending artifact retirement for %s: %w", capture, err))
		}
	}
	return result
}

// RecoverPendingRetirementsPage retries one bounded slice of the durable
// retirement journal. The cursor advances after each attempted record even if
// its backend delete fails, so one unavailable object cannot starve later
// cleanup records. Failed records are revisited after the caller wraps to the
// beginning of the journal.
func (j *linuxNativeSnapshotPublicationJournal) RecoverPendingRetirementsPage(ctx context.Context, backend storage.StorageBackend,
	after string, limit int) (NativeQualificationArtifactRetirementPage, error) {
	var page NativeQualificationArtifactRetirementPage
	if backend == nil || limit < 1 || limit > api.NativeSnapshotPublicationRecoveryBatchMax || after != "" && !canonicalNativeHelperID(after) {
		return page, state.ErrInvalidArgument
	}
	captures, err := j.pendingRetirementCaptures(ctx)
	if err != nil {
		return page, err
	}
	start := sort.SearchStrings(captures, after)
	for start < len(captures) && captures[start] <= after {
		start++
	}
	end := min(start+limit, len(captures))
	page.More = end < len(captures)
	var result error
	for _, capture := range captures[start:end] {
		if err := ctx.Err(); err != nil {
			page.More = true
			return page, errors.Join(err)
		}
		page.Examined++
		page.NextCursor = capture
		if err := j.RetireRestoreCohort(ctx, capture, backend); err != nil {
			page.More = page.More || page.Examined < end-start
			result = errors.Join(result, fmt.Errorf("native snapshot publication: recover pending artifact retirement for %s: %w", capture, err))
		}
	}
	return page, result
}

func (j *linuxNativeSnapshotPublicationJournal) pendingRetirementCaptures(ctx context.Context) ([]string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.checkLocked(); err != nil {
		return nil, err
	}
	if err := j.inventoryLocked(ctx); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(j.owner.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	directory := os.NewFile(uintptr(fd), "native-capture-retirement-recovery")
	entries, readErr := directory.ReadDir(-1)
	if err := errors.Join(readErr, directory.Close()); err != nil {
		return nil, err
	}
	captures := make([]string, 0)
	for _, entry := range entries {
		capture, complete, ok := nativePublicationRetirementEntry(entry.Name())
		if ok && !complete {
			captures = append(captures, capture)
		}
	}
	sort.Strings(captures)
	return captures, ctx.Err()
}

func (j *linuxNativeSnapshotPublicationJournal) writeRetirementStateLocked(ctx context.Context, intent nativeSnapshotPublicationIntent,
	objects [4]nativeSnapshotPublicationObjectReceipt, complete bool) (err error) {
	if _, exists, err := j.readRetirementStateLocked(ctx, intent, objects, complete); err != nil {
		return err
	} else if exists {
		return ctx.Err()
	}
	digest, err := nativePublicationObjectsDigest(objects)
	if err != nil {
		return err
	}
	fd, err := unix.Openat(int(j.owner.Fd()), ".", unix.O_TMPFILE|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "native-capture-retirement")
	defer func() { err = errors.Join(err, file.Close()) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	record := nativeSnapshotPublicationRetirement{Version: 1, Directory: j.identity,
		File: nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, IntentFile: intent.File,
		CaptureID: intent.Capture.CaptureID, ObjectsSHA256: digest}
	if err := record.validate(intent, objects); err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil || len(body) > api.NativeSnapshotPublicationRecordMaxBytes {
		return errors.Join(err, errors.New("native snapshot publication: invalid bounded retirement record"))
	}
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := errors.Join(file.Sync(), j.requireIntentLocked(ctx, intent)); err != nil {
		return err
	}
	if err := unix.Linkat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), int(j.owner.Fd()), nativePublicationRetirementName(record.CaptureID, complete), unix.AT_SYMLINK_FOLLOW); err != nil {
		if errors.Is(err, unix.EEXIST) {
			if _, exists, readErr := j.readRetirementStateLocked(ctx, intent, objects, complete); readErr == nil && exists {
				return ctx.Err()
			}
		}
		return err
	}
	return errors.Join(j.owner.Sync(), ctx.Err())
}
