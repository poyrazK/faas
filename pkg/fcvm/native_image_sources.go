package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const nativeImageRootMarker = ".gregale-image-owner.json"

type nativeImageMetadata struct {
	Mode uint32 `json:"mode"`
	UID  uint32 `json:"uid"`
	GID  uint32 `json:"gid"`
}

type nativeImageReference struct {
	ID            string             `json:"id"`
	Owner         nativeLaunchRecord `json:"owner"`
	Root          string             `json:"root"`
	Name          string             `json:"name"`
	Link          bool               `json:"link"`
	ReadOnly      bool               `json:"read_only"`
	AddPerms      uint32             `json:"add_perms"`
	Target        nativeLoopIdentity `json:"target"`
	MountID       uint64             `json:"mount_id"`
	Ready         bool               `json:"ready"`
	TargetRemoved bool               `json:"target_removed"`
	Removed       bool               `json:"removed"`
}

// An attached private anchor pins the original inode and permits restoration
// through an FD after the input pathname disappears. Epochs never reuse an
// anchor path, so an old cleanup cannot mutate a later use of the same inode.
type nativeImageSourceRecord struct {
	Epoch       string                 `json:"epoch"`
	KernelBoot  string                 `json:"kernel_boot_id"`
	Namespace   nativeLoopIdentity     `json:"namespace"`
	Identity    nativeLoopIdentity     `json:"identity"`
	Original    nativeImageMetadata    `json:"original"`
	Applied     nativeImageMetadata    `json:"applied"`
	Desired     nativeImageMetadata    `json:"desired"`
	Placeholder nativeLoopIdentity     `json:"placeholder"`
	MountID     uint64                 `json:"mount_id"`
	Ready       bool                   `json:"ready"`
	Removed     bool                   `json:"removed"`
	References  []nativeImageReference `json:"references"`
}

func (r *nativeImageSourceRecord) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, []string{"epoch", "kernel_boot_id", "namespace", "identity", "original", "applied", "desired", "placeholder", "mount_id", "ready", "removed", "references"})
	if err != nil {
		return err
	}
	for _, name := range []string{"namespace", "identity", "placeholder"} {
		if _, err := nativeJournalObjectFields(fields[name], []string{"device", "inode"}); err != nil {
			return err
		}
	}
	for _, name := range []string{"original", "applied", "desired"} {
		if _, err := nativeJournalObjectFields(fields[name], []string{"mode", "uid", "gid"}); err != nil {
			return err
		}
	}
	var refs []json.RawMessage
	if err := json.Unmarshal(fields["references"], &refs); err != nil {
		return err
	}
	for _, ref := range refs {
		value, err := nativeJournalObjectFields(ref, []string{"id", "owner", "root", "name", "link", "read_only", "add_perms", "target", "mount_id", "ready", "target_removed", "removed"})
		if err != nil {
			return err
		}
		if _, err := nativeJournalObjectFields(value["target"], []string{"device", "inode"}); err != nil {
			return err
		}
	}
	type plain nativeImageSourceRecord
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*r = nativeImageSourceRecord(decoded)
	return nil
}

func nativeImageSourceKey(identity nativeLoopIdentity) string {
	return strconv.FormatUint(identity.Device, 16) + "-" + strconv.FormatUint(identity.Inode, 16)
}

func (r nativeImageSourceRecord) validate(boot string) error {
	if !canonicalNativeHelperID(r.Epoch) || r.KernelBoot != boot || r.Identity.Inode == 0 || r.Namespace.Inode == 0 || r.Original.Mode&^0o777 != 0 || r.Applied.Mode&^0o777 != 0 || r.Desired.Mode&^0o777 != 0 || r.Ready && (r.Placeholder.Inode == 0 || r.MountID == 0) || len(r.References) == 0 {
		return errors.New("native image source: invalid epoch or inode authority")
	}
	ids := make(map[string]bool, len(r.References))
	for _, ref := range r.References {
		if err := ref.Owner.validate(ref.Owner.Lease.Instance); err != nil {
			return err
		}
		if !canonicalNativeHelperID(ref.ID) || ids[ref.ID] || ref.Owner.KernelBootID != boot || ref.Owner.Authorized || ref.Owner.Revoked || ref.Owner.ResourcesRemoved || !filepath.IsAbs(ref.Root) || filepath.Clean(ref.Root) != ref.Root || ref.Root == "/" || filepath.Base(filepath.Dir(ref.Root)) != ref.Owner.Lease.Instance || filepath.Base(ref.Root) != "root" || ref.Name == "." || ref.Name == ".." || ref.Name == "" || filepath.Base(ref.Name) != ref.Name || strings.ContainsAny(ref.Name, "\\\x00") || ref.ReadOnly && ref.AddPerms != 0o044 || !ref.ReadOnly && ref.AddPerms != 0 || ref.Removed && !ref.TargetRemoved || ref.Ready && (!r.Ready || ref.Target.Inode == 0 || !ref.Link && ref.MountID == 0) || ref.Link && ref.MountID != 0 || r.Removed && !ref.TargetRemoved {
			return errors.New("native image source: invalid reference authority")
		}
		ids[ref.ID] = true
	}
	desired, err := desiredNativeImageMetadata(r)
	if err != nil || desired != r.Desired || (r.Removed || !r.Ready) && r.Applied != r.Original {
		return errors.New("native image source: metadata target differs from retained references")
	}
	return nil
}

type nativeImagePreparation interface {
	Identity() nativeLoopIdentity
	Metadata() (nativeImageMetadata, error)
	Namespace() nativeLoopIdentity
	PreferLink() bool
	CreateAnchor(string, func(nativeLoopIdentity) error) (uint64, error)
	CreateReference(nativeImageReference, func(nativeLoopIdentity) error) (nativeImageReference, error)
	Close() error
}

type nativeImageSourceBackend interface {
	Prepare(nativeLaunchRecord, string, string, string, bool) (nativeImagePreparation, error)
	ApplyMetadata(nativeImageSourceRecord, string) error
	RetireAnchor(nativeImageSourceRecord, string) error
	CheckAnchor(nativeImageSourceRecord, string) error
	RetireReference(nativeImageSourceRecord, nativeImageReference) error
	CheckReference(nativeImageSourceRecord, nativeImageReference) error
	Inventory(string, []nativeImageSourceRecord) error
}

type nativeWritableImageBackend interface {
	PrepareWritable(context.Context, nativeLaunchRecord, string, string, string) (nativeImagePreparation, error)
}

type nativeImageSourceJournal struct {
	owner        *nativeLaunchJournal
	backend      nativeImageSourceBackend
	helperGroups nativeHostHelperGroups
	writeValue   func(string, nativeImageSourceRecord) error
}

func (j *nativeImageSourceJournal) root() string { return filepath.Join(j.owner.root, "image-sources") }

func (j *nativeImageSourceJournal) path(record nativeImageSourceRecord) string {
	return filepath.Join(j.root(), nativeImageSourceKey(record.Identity)+"."+record.Epoch+".json")
}

func (j *nativeImageSourceJournal) anchor(record nativeImageSourceRecord) string {
	return filepath.Join(j.root(), "points", record.Epoch)
}

func (j *nativeImageSourceJournal) lock(ctx context.Context, identity nativeLoopIdentity) (*os.File, error) {
	for _, root := range []string{j.root(), filepath.Join(j.root(), "points")} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return nil, err
		}
		if err := checkNativeJournalPath(root, true); err != nil {
			return nil, err
		}
	}
	return lockNativeJournalFile(ctx, filepath.Join(j.root(), nativeImageSourceKey(identity)+".lock"))
}

func (j *nativeImageSourceJournal) write(record nativeImageSourceRecord) error {
	boot, err := j.owner.currentBootID()
	if err != nil {
		return err
	}
	if err := record.validate(boot); err != nil {
		return err
	}
	if j.writeValue != nil {
		return j.writeValue(j.path(record), record)
	}
	return writeNativeJournalValue(j.path(record), record)
}

func (j *nativeImageSourceJournal) records() ([]nativeImageSourceRecord, error) {
	if _, err := os.Lstat(j.root()); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(j.root(), true); err != nil {
		return nil, err
	}
	boot, err := j.owner.currentBootID()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(j.root())
	if err != nil {
		return nil, err
	}
	var records []nativeImageSourceRecord
	active := make(map[string]bool)
	epochs := make(map[string]bool)
	for _, entry := range entries {
		if entry.Name() == "points" && entry.IsDir() || strings.HasPrefix(entry.Name(), ".launch-") && entry.Type().IsRegular() || strings.HasSuffix(entry.Name(), ".lock") && entry.Type().IsRegular() {
			continue
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, errors.New("native image source: unexpected journal entry")
		}
		path := filepath.Join(j.root(), entry.Name())
		if err := checkNativeJournalPath(path, false); err != nil {
			return nil, err
		}
		file, err := openNativeJournalFile(path, os.O_RDONLY)
		if err != nil {
			return nil, err
		}
		var record nativeImageSourceRecord
		decoder := json.NewDecoder(file)
		decodeErr := decoder.Decode(&record)
		if decodeErr == nil {
			if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
				decodeErr = errors.New("native image source: trailing ownership data")
			}
		}
		if err := errors.Join(decodeErr, file.Close()); err != nil {
			return nil, err
		}
		if err := record.validate(boot); err != nil {
			return nil, err
		}
		if j.path(record) != path || epochs[record.Epoch] {
			return nil, errors.New("native image source: duplicate or changed epoch identity")
		}
		epochs[record.Epoch] = true
		key := nativeImageSourceKey(record.Identity)
		if !record.Removed && active[key] {
			return nil, errors.New("native image source: multiple live epochs own one inode")
		}
		active[key] = active[key] || !record.Removed
		records = append(records, record)
	}
	points := filepath.Join(j.root(), "points")
	if err := checkNativeJournalPath(points, true); err != nil {
		return nil, err
	}
	entries, err = os.ReadDir(points)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !epochs[entry.Name()] {
			return nil, errors.New("native image source: anchor has no ownership epoch")
		}
	}
	return records, nil
}

func desiredNativeImageMetadata(record nativeImageSourceRecord) (nativeImageMetadata, error) {
	desired := record.Original
	var live []nativeImageReference
	for _, ref := range record.References {
		if !ref.TargetRemoved {
			live = append(live, ref)
		}
	}
	for _, ref := range live {
		if !ref.ReadOnly {
			if len(live) != 1 {
				return desired, errors.New("native image source: writable image cannot share another reference")
			}
			return nativeImageMetadata{Mode: 0o600, UID: uint32(ref.Owner.Lease.UID), GID: uint32(ref.Owner.Lease.GID)}, nil
		}
		desired.Mode |= ref.AddPerms
	}
	return desired, nil
}

// Lock order is VM, then source. Source operations never acquire another VM's
// lock. This serializes grants across image aliases and across different VMs.
func (j *nativeImageSourceJournal) stage(ctx context.Context, expected nativeLaunchRecord, root, source, name string, readOnly bool, addPerms uint32, preferLink bool) (staged string, err error) {
	if j.backend == nil {
		return "", errors.New("native image source: native backend is unavailable")
	}
	return j.stagePrepared(ctx, expected, root, name, readOnly, addPerms, func(owner nativeLaunchRecord) (nativeImagePreparation, error) {
		return j.backend.Prepare(owner, root, source, name, preferLink)
	})
}

func (j *nativeImageSourceJournal) stageWritable(ctx context.Context, expected nativeLaunchRecord, root, source, name string) (string, error) {
	if expected.Authorized || expected.Revoked || expected.ExitConfirmed || expected.ResourcesRemoved || expected.Lease.IsBuilder || name != layerImageName {
		return "", errors.New("native image source: private clone requires an original prepared app drive")
	}
	backend, ok := j.backend.(nativeWritableImageBackend)
	if !ok {
		return "", errors.New("native image source: private writable producer is unavailable")
	}
	return j.stagePrepared(ctx, expected, root, name, false, 0, func(owner nativeLaunchRecord) (nativeImagePreparation, error) {
		return backend.PrepareWritable(ctx, owner, root, source, name)
	})
}

func (j *nativeImageSourceJournal) stagePrepared(ctx context.Context, expected nativeLaunchRecord, root, name string, readOnly bool, addPerms uint32, prepare func(nativeLaunchRecord) (nativeImagePreparation, error)) (staged string, err error) {
	return j.stageOwned(ctx, expected, root, name, readOnly, addPerms, func(owner nativeLaunchRecord) (nativeLaunchRecord, error) {
		if owner.Authorized || owner.Revoked || owner.ResourcesRemoved {
			return owner, errors.New("native image source: prepared producer authority changed")
		}
		return owner, nil
	}, prepare)
}

// Only explicit, private producers may supply a different authority check.
// Ordinary staging retains the prepared-only check above.
func (j *nativeImageSourceJournal) stageOwned(ctx context.Context, expected nativeLaunchRecord, root, name string, readOnly bool, addPerms uint32, authority func(nativeLaunchRecord) (nativeLaunchRecord, error), prepare func(nativeLaunchRecord) (nativeImagePreparation, error)) (staged string, err error) {
	vmLock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, vmLock.Close()) }()
	owner, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return "", err
	}
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || !sameNativePhysicalLease(owner.Lease, expected.Lease) {
		return "", errors.New("native image source: prepared producer authority changed")
	}
	referenceOwner, err := authority(owner)
	if err != nil {
		return "", err
	}
	loops := nativeLoopMountJournal{owner: j.owner, backend: j.owner.loopMounts}
	if err := loops.requireRetired(owner); err != nil {
		return "", err
	}
	if j.owner.loopMounts != nil {
		if err := loops.requireRemoved(owner); err != nil {
			return "", err
		}
	}
	helpers := nativeHostHelperJournal{owner: j.owner, groups: j.helperGroups}
	if err := helpers.requireRemoved(owner); err != nil {
		return "", err
	}
	tun := nativeTunBindJournal{owner: j.owner, backend: j.owner.tunBinds}
	if err := tun.require(owner, false); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Refuse an interrupted earlier staging operation before allocating a new
	// anonymous clone. Its incomplete anchor/reference remains recovery work.
	if err := j.requireUnusedTarget(owner, root, name); err != nil {
		return "", err
	}
	preparation, err := prepare(referenceOwner)
	if err != nil {
		if preparation != nil {
			err = errors.Join(err, preparation.Close())
		}
		return "", err
	}
	if preparation == nil {
		return "", errors.New("native image source: producer returned no pinned preparation")
	}
	var sourceLock *os.File
	defer func() {
		// Close producer descriptors before releasing source or physical
		// ownership, including when a preparation/checkpoint failed.
		err = errors.Join(err, preparation.Close())
		if sourceLock != nil {
			err = errors.Join(err, sourceLock.Close())
		}
	}()
	sourceLock, err = j.lock(ctx, preparation.Identity())
	if err != nil {
		return "", err
	}
	records, err := j.records()
	if err != nil {
		return "", err
	}
	var record nativeImageSourceRecord
	for _, candidate := range records {
		if candidate.Identity == preparation.Identity() && !candidate.Removed {
			record = candidate
		}
		for _, ref := range candidate.References {
			if ref.Owner.Generation == owner.Generation && ref.Root == root && ref.Name == name && !ref.Removed {
				return "", errors.New("native image source: target retains an earlier staging reference")
			}
		}
	}
	if record.Epoch == "" {
		// A prior epoch may retire after Prepare pins the inode but before
		// this source lock is acquired. Capture the baseline only now.
		metadata, err := preparation.Metadata()
		if err != nil {
			return "", err
		}
		record = nativeImageSourceRecord{Epoch: uuid.NewString(), KernelBoot: owner.KernelBootID, Namespace: preparation.Namespace(), Identity: preparation.Identity(), Original: metadata, Applied: metadata, Desired: metadata}
	} else if record.Namespace != preparation.Namespace() || !record.Ready || record.Applied != record.Desired {
		return "", errors.New("native image source: previous source producer is unfinished")
	}
	if record.Ready {
		if err := j.backend.CheckAnchor(record, j.anchor(record)); err != nil {
			return "", err
		}
	}
	ref := nativeImageReference{ID: uuid.NewString(), Owner: referenceOwner, Root: root, Name: name, ReadOnly: readOnly, AddPerms: addPerms, Link: preparation.PreferLink()}
	record.References = append(record.References, ref)
	record.Desired, err = desiredNativeImageMetadata(record)
	if err != nil {
		return "", err
	}
	if err := j.write(record); err != nil {
		return "", err
	}
	index := len(record.References) - 1
	// Failures retain the frame. Native stop owns its recovery and cannot
	// discard an uncertain mount or source-permission transition.
	if !record.Ready {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		record.MountID, err = preparation.CreateAnchor(j.anchor(record), func(identity nativeLoopIdentity) error {
			record.Placeholder = identity
			return j.write(record)
		})
		if err != nil {
			return "", err
		}
		record.Ready = true
		if err := j.write(record); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := j.backend.ApplyMetadata(record, j.anchor(record)); err != nil {
		return "", err
	}
	record.Applied = record.Desired
	if err := j.write(record); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ref, err = preparation.CreateReference(ref, func(identity nativeLoopIdentity) error {
		record.References[index].Target = identity
		return j.write(record)
	})
	if err != nil {
		return "", err
	}
	ref.Ready = true
	record.References[index] = ref
	if err := j.write(record); err != nil {
		return "", err
	}
	return name, ctx.Err()
}

func (j *nativeImageSourceJournal) requireUnusedTarget(owner nativeLaunchRecord, root, name string) error {
	records, err := j.records()
	if err != nil {
		return err
	}
	for _, record := range records {
		for _, ref := range record.References {
			if ref.Owner.Generation == owner.Generation && ref.Root == root && ref.Name == name && !ref.Removed {
				return errors.New("native image source: target retains an earlier staging reference")
			}
		}
	}
	return nil
}

func sameNativeImageOwner(ref nativeImageReference, owner nativeLaunchRecord) bool {
	return ref.Owner.Generation == owner.Generation && ref.Owner.KernelBootID == owner.KernelBootID && sameNativePhysicalLease(ref.Owner.Lease, owner.Lease)
}

func (j *nativeImageSourceJournal) require(ctx context.Context, owner nativeLaunchRecord, removed bool) error {
	records, err := j.records()
	if err != nil {
		return err
	}
	for _, record := range records {
		owned := false
		for _, ref := range record.References {
			owned = owned || sameNativeImageOwner(ref, owner)
		}
		if !owned {
			continue
		}
		if err := j.requireSource(ctx, owner, record, removed); err != nil {
			return err
		}
	}
	return nil
}

func (j *nativeImageSourceJournal) requireSource(ctx context.Context, owner nativeLaunchRecord, original nativeImageSourceRecord, removed bool) (err error) {
	lock, err := j.lock(ctx, original.Identity)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	records, err := j.records()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Epoch != original.Epoch {
			continue
		}
		for _, ref := range record.References {
			if !sameNativeImageOwner(ref, owner) {
				continue
			}
			if removed && !ref.Removed || !removed && (!ref.Ready || ref.TargetRemoved || record.Removed || record.Applied != record.Desired) {
				return errors.New("native image source: reference has no required preparation or retirement proof")
			}
			if j.backend != nil {
				if err := j.backend.CheckReference(record, ref); err != nil {
					return err
				}
				if err := j.backend.CheckAnchor(record, j.anchor(record)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return errors.New("native image source: original source epoch disappeared")
}

func (j *nativeImageSourceJournal) retireAll(ctx context.Context, expected nativeLaunchRecord) (err error) {
	vmLock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, vmLock.Close()) }()
	owner, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || !sameNativePhysicalLease(owner.Lease, expected.Lease) || !owner.Revoked || !owner.ExitConfirmed {
		return errors.New("native image source: retirement lacks original exited VM authority")
	}
	records, err := j.records()
	if err != nil {
		return err
	}
	for _, original := range records {
		if err := j.retireSource(ctx, owner, original); err != nil {
			return err
		}
	}
	return nil
}

func (j *nativeImageSourceJournal) retireSource(ctx context.Context, owner nativeLaunchRecord, original nativeImageSourceRecord) (err error) {
	owned := false
	for _, ref := range original.References {
		owned = owned || sameNativeImageOwner(ref, owner)
	}
	if !owned {
		return nil
	}
	if j.backend == nil {
		return errors.New("native image source: retirement backend is unavailable")
	}
	lock, err := j.lock(ctx, original.Identity)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	records, err := j.records()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Epoch == original.Epoch {
			return j.retireSourceLocked(ctx, owner, record)
		}
	}
	return errors.New("native image source: original epoch disappeared")
}

func (j *nativeImageSourceJournal) retireSourceLocked(ctx context.Context, owner nativeLaunchRecord, record nativeImageSourceRecord) error {
	if record.Removed {
		if err := j.backend.CheckAnchor(record, j.anchor(record)); err != nil {
			return err
		}
	}
	// Preserve the previously published transition until it is confirmed.
	// Overwriting Desired first would lose proof of a chmod/chown that
	// completed immediately before its Applied acknowledgement failed.
	if record.Ready && !record.Removed && record.Applied != record.Desired {
		if err := j.backend.ApplyMetadata(record, j.anchor(record)); err != nil {
			return err
		}
		record.Applied = record.Desired
		if err := j.write(record); err != nil {
			return err
		}
	}
	for i, ref := range record.References {
		if !sameNativeImageOwner(ref, owner) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if ref.Removed {
			if err := j.backend.CheckReference(record, ref); err != nil {
				return err
			}
			continue
		}
		if err := j.backend.RetireReference(record, ref); err != nil {
			return err
		}
		record.References[i].TargetRemoved = true
	}
	desired, err := desiredNativeImageMetadata(record)
	if err != nil {
		return err
	}
	record.Desired = desired
	if err := j.write(record); err != nil {
		return err
	}
	allGone := true
	for _, ref := range record.References {
		allGone = allGone && ref.TargetRemoved
	}
	// After the original metadata acknowledgement, a crash may leave only
	// anchor removal unfinished. Do not require reopening an already unmounted
	// anchor: its original placeholder and absence remain recoverable.
	if record.Ready && !record.Removed && (!allGone || record.Applied != record.Desired) {
		if err := j.backend.ApplyMetadata(record, j.anchor(record)); err != nil {
			return err
		}
		record.Applied = record.Desired
		if err := j.write(record); err != nil {
			return err
		}
	}
	if allGone && !record.Removed {
		if err := j.backend.RetireAnchor(record, j.anchor(record)); err != nil {
			return err
		}
		record.Removed = true
	}
	for i, ref := range record.References {
		if sameNativeImageOwner(ref, owner) {
			record.References[i].Removed = true
		}
	}
	return j.write(record)
}

func (j *nativeImageSourceJournal) inventory(ctx context.Context, owners []nativeLaunchRecord) error {
	records, err := j.records()
	if err != nil {
		return err
	}
	if err := j.validateOwners(records, owners); err != nil {
		return err
	}
	for _, record := range records {
		for _, ref := range record.References {
			if j.backend != nil && ref.Removed {
				if err := j.backend.CheckReference(record, ref); err != nil {
					return err
				}
			}
		}
	}
	if j.backend == nil {
		if len(records) != 0 {
			return errors.New("native image source: native inventory backend is unavailable")
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return j.backend.Inventory(j.root(), records)
}

func (j *nativeImageSourceJournal) validateOwners(records []nativeImageSourceRecord, owners []nativeLaunchRecord) error {
	byGeneration := make(map[string]nativeLaunchRecord, len(owners))
	for _, owner := range owners {
		byGeneration[owner.Generation] = owner
	}
	for _, record := range records {
		for _, ref := range record.References {
			owner, current := byGeneration[ref.Owner.Generation]
			if !current {
				helper := nativeHostHelperJournal{owner: j.owner}
				archived, err := helper.archivedOwner(ref.Owner.Generation)
				if err != nil {
					return err
				}
				owner = archived
			}
			if !sameNativeImageOwner(ref, owner) || (!current || owner.ResourcesRemoved) && !ref.Removed {
				return errors.New("native image source: reference outlived its original owner")
			}
		}
	}
	return nil
}
