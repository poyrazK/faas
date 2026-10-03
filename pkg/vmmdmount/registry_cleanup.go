package vmmdmount

// adr: 435. Keep cleanup ownership until both mount and source are released.

import (
	"context"
	"errors"
	"fmt"
	"os"
)

func cleanupRegisteredMount(ctx context.Context, mountpoint string, entry MountEntry, unmounted bool) (bool, error) {
	if !unmounted {
		var err error
		switch entry.Kind {
		case MountKindParentExt4:
			err = UmountExt4(ctx, mountpoint)
		case MountKindOverlayParent:
			err = UmountOverlayParent(ctx, mountpoint)
		default:
			return false, fmt.Errorf("vmmdmount: registry.Umount: unknown MountKind=%d", entry.Kind)
		}
		if err != nil && !errors.Is(err, ErrUnknownMountpoint) && !errors.Is(err, errMountDirectoryCleanup) {
			return false, err
		}
		unmounted = true
	}
	return unmounted, cleanupRegisteredFiles(ctx, mountpoint, entry)
}

var errMountDirectoryCleanup = errors.New("vmmdmount: released mount directory cleanup failed")

func removeReleasedMountpoint(mountpoint string) error {
	if err := os.Remove(mountpoint); err != nil && !os.IsNotExist(err) {
		return errors.Join(errMountDirectoryCleanup, err)
	}
	return nil
}

func cleanupRegisteredFiles(ctx context.Context, mountpoint string, entry MountEntry) error {
	if err := removeReleasedMountpoint(mountpoint); err != nil {
		return err
	}
	if entry.Kind == MountKindParentExt4 && entry.SrcPath != "" {
		if err := os.Remove(entry.SrcPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("vmmdmount: remove registered source: %w", err)
		}
	}
	return ctx.Err()
}

func (r *Registry) umount(ctx context.Context, mountpoint string, owner *MountLease) (bool, error) {
	r.mu.Lock()
	entry, found := r.entries[mountpoint]
	if !found {
		r.mu.Unlock()
		return false, nil
	}
	if r.releasing[mountpoint] || r.owners[mountpoint] != owner {
		r.mu.Unlock()
		return false, ErrMountBusy
	}
	r.releasing[mountpoint] = true
	unmounted := r.unmounted[mountpoint]
	r.mu.Unlock()

	unmounted, err := r.cleanup(ctx, mountpoint, entry, unmounted)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.releasing, mountpoint)
	if err == nil {
		delete(r.entries, mountpoint)
		delete(r.retrying, mountpoint)
		delete(r.unmounted, mountpoint)
	} else {
		r.retrying[mountpoint] = true
		r.unmounted[mountpoint] = unmounted
	}
	if owner != nil {
		delete(r.owners, mountpoint)
		owner.closed, owner.cleanupErr = true, err
	}
	return err == nil, err
}
