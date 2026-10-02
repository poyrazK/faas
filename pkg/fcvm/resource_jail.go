// adr: 401
package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func resourceDirectoryID(path string) (*resourceFileIdentity, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || s.Ino == 0 {
		return nil, errors.New("jail path must be a directory, without a symlink")
	}
	return &resourceFileIdentity{Device: uint64(s.Dev), Inode: s.Ino}, nil
}

func syncResourceParent(path string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(journalSyncDirectory(root), root.Close())
}

func (v *JailerVMM) jailAssets(instance string) []resourceAsset {
	v.mu.Lock()
	defer v.mu.Unlock()
	return cloneResourceAssets(v.ownedJails[instance])
}

func (v *JailerVMM) rememberJail(instance string, a resourceAsset) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ownedJails == nil {
		v.ownedJails = make(map[string][]resourceAsset)
	}
	for i := range v.ownedJails[instance] {
		if v.ownedJails[instance][i].Path == a.Path {
			v.ownedJails[instance][i] = a
			return
		}
	}
	v.ownedJails[instance] = append(v.ownedJails[instance], a)
}

func (v *JailerVMM) makeOwnedJail(instance string) (string, error) {
	// Retry may only replace a jail observed by this VMM, after bind cleanup.
	if err := v.checkOwnedJail(instance); err != nil {
		return "", fmt.Errorf("vmm: wipe stale chroot: %w", err)
	}
	if err := v.unmountBindMounts(instance); err != nil {
		return "", err
	}
	if err := v.removeOwnedJail(instance); err != nil {
		return "", fmt.Errorf("vmm: wipe stale chroot: %w", err)
	}
	if err := os.MkdirAll(v.JailRoot(), 0o755); err != nil {
		return "", fmt.Errorf("vmm: mkdir chroot parent: %w", err)
	}
	parent, err := filepath.EvalSymlinks(v.JailRoot())
	if err != nil {
		return "", err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	context, err := resourcePlacementContext()
	if err != nil {
		return "", err
	}
	anchor := filepath.Join(parent, instance)
	root := filepath.Join(anchor, "root")
	for _, path := range []string{anchor, root} {
		if err := v.makeJailDirectory(instance, resourceAsset{Kind: "jail", Path: path, Namespace: context}); err != nil {
			return "", err
		}
	}
	return root, nil
}

func (v *JailerVMM) makeJailDirectory(instance string, a resourceAsset) error {
	v.rememberJail(instance, a)
	if err := v.resourceJournal.addAsset(instance, a); err != nil {
		return err
	}
	if err := os.Mkdir(a.Path, 0o755); err != nil {
		return fmt.Errorf("vmm: mkdir chroot: %w", err)
	}
	identity, err := resourceDirectoryID(a.Path)
	if err != nil || identity == nil {
		return errors.Join(errors.New("capture created jail directory"), err)
	}
	a.File = identity
	v.rememberJail(instance, a) // Keep live provenance even when fsync/checkpoint fails.
	if err := syncResourceParent(a.Path); err != nil {
		return err
	}
	return v.resourceJournal.checkpointAsset(instance, a.Path, *identity, nil)
}

func checkJailAsset(a resourceAsset) error {
	if a.Namespace != nil {
		current, err := resourceMountNamespace()
		if err != nil {
			return err
		}
		if current != *a.Namespace {
			return errors.New("jail mount namespace context changed")
		}
	}
	actual, err := resourceDirectoryID(a.Path)
	if err != nil || actual == nil {
		return err
	}
	if a.File == nil || *actual != *a.File {
		return errors.New("jail directory identity changed or unknown")
	}
	return nil
}

func (v *JailerVMM) checkOwnedJail(instance string) error {
	if v.resourceJournal == nil {
		return nil
	}
	assets := v.jailAssets(instance)
	if len(assets) == 0 {
		identity, err := resourceDirectoryID(filepath.Dir(v.chrootRoot(instance)))
		if err != nil {
			return err
		}
		if identity != nil {
			return errors.New("jail exists without a live cleanup owner")
		}
	}
	for _, a := range assets {
		if err := checkJailAsset(a); err != nil {
			return fmt.Errorf("check jail %s: %w", a.Path, err)
		}
	}
	return nil
}

func (v *JailerVMM) removeOwnedJail(instance string) error {
	if v.resourceJournal == nil {
		return os.RemoveAll(filepath.Dir(v.chrootRoot(instance)))
	}
	if err := v.checkOwnedJail(instance); err != nil {
		return err
	}
	assets := v.jailAssets(instance)
	if len(assets) == 0 {
		return nil
	}
	anchor := assets[0].Path
	if err := resourceMountTreeClear(anchor); err != nil {
		return err
	}
	if err := os.RemoveAll(anchor); err != nil {
		return err
	}
	if err := syncResourceParent(anchor); err != nil {
		return err
	}
	for _, a := range assets {
		if err := v.resourceJournal.retireAsset(instance, a.Path); err != nil {
			return err
		}
	}
	v.mu.Lock()
	delete(v.ownedJails, instance)
	v.mu.Unlock()
	return nil
}
