package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const nativeTunTargetName = "faas-host-tun"
const nativeTunRdev = uint64(10<<8 | 200)

type nativeTunSource struct {
	Identity nativeLoopIdentity `json:"identity"`
	Rdev     uint64             `json:"rdev"`
	Mode     uint32             `json:"mode"`
}

// This records the host-namespace bind inherited by jailer, separately from
// the scoped jail-device helper and its private namespace effects.
type nativeTunBindRecord struct {
	Owner       nativeLaunchRecord `json:"owner"`
	Root        string             `json:"root"`
	Namespace   nativeLoopIdentity `json:"namespace"`
	Source      nativeTunSource    `json:"source"`
	Placeholder nativeLoopIdentity `json:"placeholder"`
	MountID     uint64             `json:"mount_id"`
	Ready       bool               `json:"ready"`
	Removed     bool               `json:"removed"`
}

func (r *nativeTunBindRecord) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, []string{"owner", "root", "namespace", "source", "placeholder", "mount_id", "ready", "removed"})
	if err != nil {
		return err
	}
	source, err := nativeJournalObjectFields(fields["source"], []string{"identity", "rdev", "mode"})
	if err != nil {
		return err
	}
	for _, value := range []json.RawMessage{fields["namespace"], fields["placeholder"], source["identity"]} {
		if _, err := nativeJournalObjectFields(value, []string{"device", "inode"}); err != nil {
			return err
		}
	}
	type plain nativeTunBindRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(r))
}

func (r nativeTunBindRecord) validate(boot string) error {
	if err := r.Owner.validate(r.Owner.Lease.Instance); err != nil {
		return err
	}
	if r.Owner.KernelBootID != boot || r.Owner.Authorized || r.Owner.Revoked || r.Owner.ResourcesRemoved || r.Owner.Lease.Networkless || !filepath.IsAbs(r.Root) || filepath.Clean(r.Root) != r.Root || filepath.Base(r.Root) != "root" || filepath.Base(filepath.Dir(r.Root)) != r.Owner.Lease.Instance || !nativeExecutableName(filepath.Base(filepath.Dir(filepath.Dir(r.Root))), "firecracker") || r.Namespace.Inode == 0 || r.Source.Identity.Inode == 0 || r.Source.Rdev != nativeTunRdev || r.Source.Mode&^0o777 != 0 || r.Source.Mode&0o006 != 0o006 || (r.Placeholder.Inode == 0) != (r.Placeholder.Device == 0) || r.Ready && (r.Placeholder.Inode == 0 || r.MountID == 0) || r.MountID != 0 && r.Placeholder.Inode == 0 {
		return errors.New("native TUN bind: incomplete original device or prepared jail authority")
	}
	return nil
}

type nativeTunPreparation interface {
	Source() nativeTunSource
	Namespace() nativeLoopIdentity
	Bind(func(nativeLoopIdentity) error) (uint64, error)
	Close() error
}

type nativeTunBindBackend interface {
	Prepare(nativeLaunchRecord, string) (nativeTunPreparation, error)
	Check(nativeTunBindRecord, bool) error
	Retire(nativeTunBindRecord) error
	Inventory([]nativeTunBindRecord) error
}

type nativeTunBindJournal struct {
	owner        *nativeLaunchJournal
	backend      nativeTunBindBackend
	helperGroups nativeHostHelperGroups
	writeValue   func(string, nativeTunBindRecord) error
}

func (j *nativeTunBindJournal) root() string { return filepath.Join(j.owner.root, "tun-binds") }
func (j *nativeTunBindJournal) path(record nativeTunBindRecord) string {
	return filepath.Join(j.root(), record.Owner.Generation+".json")
}
func (j *nativeTunBindJournal) write(record nativeTunBindRecord) error {
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

func (j *nativeTunBindJournal) records() ([]nativeTunBindRecord, error) {
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
	var records []nativeTunBindRecord
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".launch-") && entry.Type().IsRegular() {
			continue
		}
		generation, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !canonicalNativeHelperID(generation) || !entry.Type().IsRegular() {
			return nil, errors.New("native TUN bind: unexpected ownership entry")
		}
		path := filepath.Join(j.root(), entry.Name())
		if err := checkNativeJournalPath(path, false); err != nil {
			return nil, err
		}
		file, err := openNativeJournalFile(path, os.O_RDONLY)
		if err != nil {
			return nil, err
		}
		var record nativeTunBindRecord
		decoder := json.NewDecoder(file)
		decodeErr := decoder.Decode(&record)
		if decodeErr == nil {
			if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
				decodeErr = errors.New("native TUN bind: trailing ownership data")
			}
		}
		if err := errors.Join(decodeErr, file.Close()); err != nil {
			return nil, err
		}
		if err := record.validate(boot); err != nil {
			return nil, err
		}
		if record.Owner.Generation != generation {
			return nil, errors.New("native TUN bind: record filename differs from original generation")
		}
		records = append(records, record)
	}
	return records, nil
}

func sameNativeTunOwner(record nativeTunBindRecord, owner nativeLaunchRecord) bool {
	return record.Owner.Generation == owner.Generation && record.Owner.KernelBootID == owner.KernelBootID && record.Owner.Lease == owner.Lease
}

// The VM lock spans the synchronous producer and every publication. Revocation
// excludes late attachment; process death leaves an original recovery frame.
func (j *nativeTunBindJournal) stage(ctx context.Context, expected nativeLaunchRecord, root string) (err error) {
	if j.backend == nil {
		return errors.New("native TUN bind: native backend is unavailable")
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
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || owner.Lease != expected.Lease || owner.Authorized || owner.Revoked || owner.ResourcesRemoved || owner.Lease.Networkless {
		return errors.New("native TUN bind: original prepared producer authority changed")
	}
	loops := nativeLoopMountJournal{owner: j.owner, backend: j.owner.loopMounts}
	if err := loops.requireRetired(owner); err != nil {
		return err
	}
	if j.owner.loopMounts != nil {
		if err := loops.requireRemoved(owner); err != nil {
			return err
		}
	}
	helpers := nativeHostHelperJournal{owner: j.owner, groups: j.helperGroups}
	if err := helpers.requireRemoved(owner); err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: j.owner, backend: j.owner.imageSources}
	if err := images.require(ctx, owner, false); err != nil {
		return err
	}
	records, err := j.records()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Owner.Generation == owner.Generation {
			return errors.New("native TUN bind: this generation already retains a binding frame")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := j.backend.Prepare(owner, root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, prepared.Close()) }()
	if err := os.MkdirAll(j.root(), 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(j.root(), true); err != nil {
		return err
	}
	record := nativeTunBindRecord{Owner: owner, Root: root, Namespace: prepared.Namespace(), Source: prepared.Source()}
	if err := j.write(record); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	record.MountID, err = prepared.Bind(func(identity nativeLoopIdentity) error {
		record.Placeholder = identity
		return j.write(record)
	})
	if err != nil {
		return err
	}
	if record.MountID == 0 {
		return errors.New("native TUN bind: attachment has no kernel mount identity")
	}
	record.Ready = true
	if err := j.backend.Check(record, false); err != nil {
		return err
	}
	if err := j.write(record); err != nil {
		return err
	}
	return ctx.Err()
}

func (j *nativeTunBindJournal) require(owner nativeLaunchRecord, removed bool) error {
	records, err := j.records()
	if err != nil {
		return err
	}
	for _, record := range records {
		if !sameNativeTunOwner(record, owner) {
			continue
		}
		if removed && !record.Removed || !removed && (!record.Ready || record.Removed) {
			return errors.New("native TUN bind: original reference has no required preparation or retirement proof")
		}
		if j.backend == nil {
			return errors.New("native TUN bind: native proof backend is unavailable")
		}
		if err := j.backend.Check(record, removed); err != nil {
			return err
		}
	}
	return nil
}

func (j *nativeTunBindJournal) retire(ctx context.Context, expected nativeLaunchRecord) (err error) {
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
		return errors.New("native TUN bind: retirement lacks original VM exit authority")
	}
	helpers := nativeHostHelperJournal{owner: j.owner, groups: j.helperGroups}
	if err := helpers.requireRemoved(owner); err != nil {
		return err
	}
	records, err := j.records()
	if err != nil {
		return err
	}
	for _, record := range records {
		if !sameNativeTunOwner(record, owner) {
			continue
		}
		if j.backend == nil {
			return errors.New("native TUN bind: native retirement backend is unavailable")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if record.Removed {
			if err := j.backend.Check(record, true); err != nil {
				return err
			}
			continue
		}
		if err := j.backend.Retire(record); err != nil {
			return err
		}
		record.Removed = true
		if err := j.backend.Check(record, true); err != nil {
			return err
		}
		if err := j.write(record); err != nil {
			return err
		}
	}
	return nil
}

func (j *nativeTunBindJournal) validateOwners(records []nativeTunBindRecord, owners []nativeLaunchRecord) error {
	byGeneration := make(map[string]nativeLaunchRecord, len(owners))
	for _, owner := range owners {
		byGeneration[owner.Generation] = owner
	}
	for _, record := range records {
		owner, current := byGeneration[record.Owner.Generation]
		if !current {
			archived, err := (&nativeHostHelperJournal{owner: j.owner}).archivedOwner(record.Owner.Generation)
			if err != nil {
				return err
			}
			owner = archived
		}
		if !sameNativeTunOwner(record, owner) || (!current || owner.ResourcesRemoved) && !record.Removed {
			return errors.New("native TUN bind: frame outlived its original owner")
		}
	}
	return nil
}

func (j *nativeTunBindJournal) inventory(ctx context.Context, owners []nativeLaunchRecord) error {
	records, err := j.records()
	if err != nil {
		return err
	}
	if err := j.validateOwners(records, owners); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.backend == nil {
		if len(records) != 0 {
			return errors.New("native TUN bind: native inventory backend is unavailable")
		}
		return nil
	}
	return j.backend.Inventory(records)
}
