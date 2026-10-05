package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func (v *JailerVMM) stageNativeImageForOwner(ctx context.Context, owner nativeLaunchRecord, root, source, name string, readOnly, preferLink bool) (string, error) {
	r := v.nativeRecovery
	if r == nil || owner.Generation != r.generation(owner.Lease.Instance) || root != v.chrootRoot(owner.Lease.Instance) {
		return "", errors.New("native image source: staging lacks original local jail authority")
	}
	perms := uint32(0)
	if readOnly {
		perms = 0o044
	}
	j := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources, helperGroups: r.helperGroups}
	return j.stage(ctx, owner, root, source, name, readOnly, perms, preferLink)
}

func (v *JailerVMM) stageNativeWritableImageForOwner(ctx context.Context, owner nativeLaunchRecord, root, source, name string, uid, gid int, instance string) (string, error) {
	r := v.nativeRecovery
	if r == nil || r.journal == nil || instance != owner.Lease.Instance || uid != owner.Lease.UID || gid != owner.Lease.GID || root != v.chrootRoot(instance) || name != layerImageName || owner.Lease.IsBuilder {
		return "", errors.New("native image source: private clone lacks original local jail authority")
	}
	r.mu.Lock()
	generation, daemonLock := r.owned[instance], r.daemonLock
	r.mu.Unlock()
	if generation != owner.Generation || generation == "" || daemonLock == nil {
		return "", errors.New("native image source: private clone lacks original daemon producer")
	}
	if _, err := daemonLock.Stat(); err != nil {
		return "", err
	}
	j := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources, helperGroups: r.helperGroups}
	return j.stageWritable(ctx, owner, root, source, name)
}

func (v *JailerVMM) mkChrootForOwner(ctx context.Context, expected nativeLaunchRecord, instance string) (root string, err error) {
	if v.nativeRecovery == nil {
		return v.mkChrootLegacy(instance)
	}
	r := v.nativeRecovery
	if expected.Lease.Instance != instance || expected.Generation != r.generation(instance) {
		return "", errors.New("native recovery: jail creation lacks original local authority")
	}
	lock, err := r.journal.lock(ctx, instance)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := r.journal.read(instance)
	if err != nil {
		return "", err
	}
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || !sameNativePhysicalLease(owner.Lease, expected.Lease) || owner.Authorized || owner.Revoked || owner.ResourcesRemoved {
		return "", errors.New("native recovery: jail producer authority changed")
	}
	root = v.chrootRoot(instance)
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		return "", errors.Join(err, errors.New("native recovery: existing jail requires retirement before creation"))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	for dir := root; dir != v.chrootBase && dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if err := os.Chmod(dir, 0o755); err != nil {
			return "", err
		}
	}
	return root, ctx.Err()
}
