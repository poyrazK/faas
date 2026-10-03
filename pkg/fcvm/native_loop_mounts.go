package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type nativeLoopIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type nativeLoopDevice struct {
	Number    int                `json:"number"`
	Rdev      uint64             `json:"rdev"`
	Source    nativeLoopIdentity `json:"source"`
	Namespace nativeLoopIdentity `json:"namespace"`
}

// The backing file and mount namespace are pinned before publishing this frame.
// No loop attachment or mount can precede its durable ownership record.
type nativeLoopMountRecord struct {
	ID        string             `json:"id"`
	Owner     nativeLaunchRecord `json:"owner"`
	Device    nativeLoopDevice   `json:"device"`
	Directory nativeLoopIdentity `json:"directory"`
	MountID   uint64             `json:"mount_id"`
	Removed   bool               `json:"removed"`
}

func (r *nativeLoopMountRecord) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, []string{"id", "owner", "device", "directory", "mount_id", "removed"})
	if err != nil {
		return err
	}
	device, err := nativeJournalObjectFields(fields["device"], []string{"number", "rdev", "source", "namespace"})
	if err != nil {
		return err
	}
	for _, value := range []json.RawMessage{fields["directory"], device["source"], device["namespace"]} {
		if _, err := nativeJournalObjectFields(value, []string{"device", "inode"}); err != nil {
			return err
		}
	}
	type plain nativeLoopMountRecord
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*r = nativeLoopMountRecord(decoded)
	return nil
}

func (r nativeLoopMountRecord) validate(owner nativeLaunchRecord) error {
	if err := r.Owner.validate(owner.Lease.Instance); err != nil {
		return err
	}
	if !canonicalNativeHelperID(r.ID) || r.Owner.Generation != owner.Generation || r.Owner.KernelBootID != owner.KernelBootID || r.Owner.Lease != owner.Lease || r.Owner.Revoked || r.Owner.Authorized || r.Owner.ResourcesRemoved {
		return errors.New("native loop mount: frame differs from its prepared VM owner")
	}
	if r.Device.Number < 0 || r.Device.Rdev == 0 || r.Device.Source.Inode == 0 || r.Device.Namespace.Inode == 0 || (r.Directory.Device == 0) != (r.Directory.Inode == 0) || r.MountID != 0 && r.Directory.Inode == 0 {
		return errors.New("native loop mount: incomplete kernel ownership")
	}
	return nil
}

// Prepare opens and pins a regular backing file and an unconfigured loop FD.
// Configure must atomically attach with AUTOCLEAR, without a helper process.
// Retire never lazily unmounts or detaches a loop with a different identity.
type nativeLoopReservation interface {
	Device() nativeLoopDevice
	Configure() error
	Mount(string) (uint64, error)
	Close() error
}

type nativeLoopMountBackend interface {
	Prepare(string, string) (nativeLoopReservation, error)
	Retire(context.Context, nativeLoopMountRecord, string) error
	Removed(nativeLoopMountRecord, string) error
	Inventory([]nativeLoopMountRecord) error
}

type nativeLoopMountJournal struct {
	owner        *nativeLaunchJournal
	backend      nativeLoopMountBackend
	helperGroups nativeHostHelperGroups
	writeValue   func(string, nativeLoopMountRecord) error
}

func (j *nativeLoopMountJournal) root(generation string) string {
	return filepath.Join(j.owner.root, "loop-mounts", generation)
}

func (j *nativeLoopMountJournal) point(record nativeLoopMountRecord) string {
	return filepath.Join(j.root(record.Owner.Generation), "points", record.ID)
}

func (j *nativeLoopMountJournal) write(owner nativeLaunchRecord, record nativeLoopMountRecord) error {
	if err := record.validate(owner); err != nil {
		return err
	}
	path := filepath.Join(j.root(owner.Generation), record.ID+".json")
	if j.writeValue != nil {
		return j.writeValue(path, record)
	}
	return writeNativeJournalValue(path, record)
}

func (j *nativeLoopMountJournal) records(owner nativeLaunchRecord) ([]nativeLoopMountRecord, error) {
	root := j.root(owner.Generation)
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	for _, path := range []string{filepath.Dir(root), root} {
		if err := checkNativeJournalPath(path, true); err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var records []nativeLoopMountRecord
	byID := make(map[string]bool)
	for _, entry := range entries {
		if entry.Name() == "points" && entry.IsDir() || strings.HasPrefix(entry.Name(), ".launch-") && entry.Type().IsRegular() {
			continue
		}
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !canonicalNativeHelperID(id) || !entry.Type().IsRegular() {
			return nil, errors.New("native loop mount: unexpected journal entry")
		}
		path := filepath.Join(root, entry.Name())
		if err := checkNativeJournalPath(path, false); err != nil {
			return nil, err
		}
		file, err := openNativeJournalFile(path, os.O_RDONLY)
		if err != nil {
			return nil, err
		}
		var record nativeLoopMountRecord
		decoder := json.NewDecoder(file)
		decodeErr := decoder.Decode(&record)
		if decodeErr == nil {
			if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
				decodeErr = errors.New("native loop mount: trailing frame data")
			}
		}
		if err := errors.Join(decodeErr, file.Close()); err != nil {
			return nil, err
		}
		if record.ID != id {
			return nil, errors.New("native loop mount: filename differs from frame identity")
		}
		if err := record.validate(owner); err != nil {
			return nil, err
		}
		byID[id] = true
		records = append(records, record)
	}
	points := filepath.Join(root, "points")
	if _, err := os.Lstat(points); errors.Is(err, os.ErrNotExist) {
		return records, nil
	} else if err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(points, true); err != nil {
		return nil, err
	}
	entries, err = os.ReadDir(points)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !byID[entry.Name()] || !entry.IsDir() {
			return nil, errors.New("native loop mount: mountpoint has no recoverable owner")
		}
	}
	return records, nil
}

func (j *nativeLoopMountJournal) requireRetired(owner nativeLaunchRecord) error {
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if !record.Removed {
			return errors.New("native loop mount: VM ownership retains an unfinished staging producer")
		}
	}
	return nil
}

func (j *nativeLoopMountJournal) requireRemoved(owner nativeLaunchRecord) error {
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if !record.Removed || j.backend == nil {
			return errors.New("native loop mount: staging cleanup is not attested")
		}
		if err := j.backend.Removed(record, j.point(record)); err != nil {
			return err
		}
	}
	return nil
}

// The lock covers synchronous Go writers as well as kernel operations. A stop
// waits for them; after daemon death, the durable frame owns any surviving mount.
func (j *nativeLoopMountJournal) session(ctx context.Context, expected nativeLaunchRecord, drive string, fn func(string) error, measured ...*loopMountTimings) (err error) {
	var timings *loopMountTimings
	var started time.Time
	mountMeasured := false
	if len(measured) > 0 && measured[0] != nil {
		timings, started = measured[0], time.Now()
		*timings = loopMountTimings{}
		defer func() {
			if !mountMeasured {
				timings.Mount = time.Since(started) - timings.Unmount
			}
		}()
	}
	if j.backend == nil || fn == nil {
		return errors.New("native loop mount: native backend and writer are required")
	}
	lock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || owner.Lease != expected.Lease || owner.Revoked || owner.Authorized || owner.ResourcesRemoved {
		return errors.New("native loop mount: prepared staging authority changed or was revoked")
	}
	if err := j.requireRemoved(owner); err != nil {
		return err
	}
	helpers := nativeHostHelperJournal{owner: j.owner, groups: j.helperGroups}
	if err := helpers.requireRemoved(owner); err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: j.owner, backend: j.owner.imageSources}
	if err := images.require(ctx, owner, false); err != nil {
		return err
	}
	id := uuid.NewString()
	reservation, err := j.backend.Prepare(drive, id)
	if err != nil {
		return err
	}
	defer func() {
		if reservation != nil {
			err = errors.Join(err, reservation.Close())
		}
	}()
	record := nativeLoopMountRecord{ID: id, Owner: owner, Device: reservation.Device()}
	for _, path := range []string{filepath.Dir(j.root(owner.Generation)), j.root(owner.Generation), filepath.Dir(j.point(record))} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if err := checkNativeJournalPath(path, true); err != nil {
			return err
		}
	}
	if err := j.write(owner, record); err != nil {
		return err
	}
	defer func() {
		var retireStarted time.Time
		if timings != nil {
			retireStarted = time.Now()
		}
		// Close the reservation before probing retirement: AUTOCLEAR can then
		// detach an attachment that never reached mount. Never remove a tree.
		closeErr := reservation.Close()
		reservation = nil
		err = errors.Join(err, closeErr, j.removeLocked(context.WithoutCancel(ctx), owner, record))
		if timings != nil {
			timings.Unmount = time.Since(retireStarted)
		}
	}()
	if err := os.Mkdir(j.point(record), 0o700); err != nil {
		return err
	}
	record.Directory, err = nativeLoopDirectoryIdentity(j.point(record))
	if err != nil {
		return err
	}
	if err := j.write(owner, record); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(j.point(record)))
	if err != nil {
		return err
	}
	if err := errors.Join(parent.Sync(), parent.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reservation.Configure(); err != nil {
		return err
	}
	record.MountID, err = reservation.Mount(j.point(record))
	if err != nil {
		return err
	}
	if record.MountID == 0 {
		return errors.New("native loop mount: mount has no kernel identity")
	}
	if err := j.write(owner, record); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if timings != nil {
		timings.Mount, mountMeasured = time.Since(started), true
	}
	return errors.Join(fn(j.point(record)), ctx.Err())
}

func (j *nativeLoopMountJournal) removeLocked(ctx context.Context, owner nativeLaunchRecord, record nativeLoopMountRecord) error {
	if j.backend == nil {
		return errors.New("native loop mount: retirement backend is unavailable")
	}
	if record.Removed {
		return j.backend.Removed(record, j.point(record))
	}
	if err := j.backend.Retire(ctx, record, j.point(record)); err != nil {
		return fmt.Errorf("native loop mount: retire staged image: %w", err)
	}
	point := j.point(record)
	if _, err := os.Lstat(point); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		identity, err := nativeLoopDirectoryIdentity(point)
		if err != nil {
			return err
		}
		if record.Directory.Inode != 0 && identity != record.Directory {
			return errors.New("native loop mount: original mountpoint directory changed")
		}
		// A crash between mkdir and identity publication cannot have mounted:
		// configuration requires the next durable write. Remove only empty,
		// private directories at the predeclared unique point.
		if err := os.Remove(point); err != nil {
			return err
		}
	}
	parent, err := os.Open(filepath.Dir(point))
	if err != nil {
		return err
	}
	if err := errors.Join(parent.Sync(), parent.Close()); err != nil {
		return err
	}
	if err := j.backend.Removed(record, point); err != nil {
		return err
	}
	record.Removed = true
	return j.write(owner, record)
}

func (j *nativeLoopMountJournal) retireAll(ctx context.Context, expected nativeLaunchRecord) (err error) {
	lock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || owner.Lease != expected.Lease || !owner.Revoked || !owner.ExitConfirmed {
		return errors.New("native loop mount: retirement has no original exited VM authority")
	}
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := j.removeLocked(ctx, owner, record); err != nil {
			return err
		}
	}
	return nil
}

func (j *nativeLoopMountJournal) allRecords(ctx context.Context, current []nativeLaunchRecord) ([]nativeLoopMountRecord, error) {
	root := filepath.Join(j.owner.root, "loop-mounts")
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(root, true); err != nil {
		return nil, err
	}
	owners := make(map[string]nativeLaunchRecord, len(current))
	for _, owner := range current {
		owners[owner.Generation] = owner
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var all []nativeLoopMountRecord
	for _, entry := range entries {
		if !entry.IsDir() || !canonicalNativeHelperID(entry.Name()) {
			return nil, errors.New("native loop mount: unexpected generation directory")
		}
		owner, active := owners[entry.Name()]
		if !active {
			helper := nativeHostHelperJournal{owner: j.owner}
			owner, err = helper.archivedOwner(entry.Name())
			if err != nil {
				return nil, err
			}
		}
		lock, err := j.owner.lock(ctx, owner.Lease.Instance)
		if err != nil {
			return nil, err
		}
		records, readErr := j.records(owner)
		if err := errors.Join(readErr, lock.Close()); err != nil {
			return nil, err
		}
		for _, record := range records {
			if (!active || owner.ResourcesRemoved) && !record.Removed {
				return nil, errors.New("native loop mount: completed owner retains unfinished staging")
			}
		}
		all = append(all, records...)
	}
	return all, ctx.Err()
}
