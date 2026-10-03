// adr: 400
// adr: 402
package fcvm

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Assets are provenance for future recovery, not recovered lifecycle ownership.
// Intent precedes creation/permission changes; checkpoints follow the syscall.
type resourceAsset struct {
	Kind         string                 `json:"kind"`
	Path         string                 `json:"path"`
	File         *resourceFileIdentity  `json:"file,omitempty"`
	Target       *resourceFileIdentity  `json:"target,omitempty"`
	Source       string                 `json:"source,omitempty"`
	SourceFile   *resourceFileIdentity  `json:"source_file,omitempty"`
	OriginalMode uint32                 `json:"original_mode,omitempty"`
	ReadOnly     bool                   `json:"read_only,omitempty"`
	Namespace    *resourceMountIdentity `json:"namespace,omitempty"`
	Mount        *resourceMountIdentity `json:"mount,omitempty"`
	Link         *resourceLinkIdentity  `json:"link,omitempty"`
}

type resourceFileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type resourceMountIdentity struct {
	BootID    string `json:"boot_id"`
	Namespace uint64 `json:"namespace"`
	MountID   uint64 `json:"mount_id,omitempty"`
}

func cloneResourceAssets(assets []resourceAsset) []resourceAsset {
	copy := append([]resourceAsset(nil), assets...)
	for i := range copy {
		if p := copy[i].Link; p != nil {
			v := *p
			copy[i].Link = &v
		}
		if p := copy[i].File; p != nil {
			v := *p
			copy[i].File = &v
		}
		if p := copy[i].Target; p != nil {
			v := *p
			copy[i].Target = &v
		}
		if p := copy[i].SourceFile; p != nil {
			v := *p
			copy[i].SourceFile = &v
		}
		if p := copy[i].Namespace; p != nil {
			v := *p
			copy[i].Namespace = &v
		}
		if p := copy[i].Mount; p != nil {
			v := *p
			copy[i].Mount = &v
		}
	}
	return copy
}

func resourceAssetPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsAny(path, "\x00\r\n")
}

func (r resourceJournalRecord) validateAssets() error {
	if len(r.Assets) > 128 || (r.Version == 1 && len(r.Assets) != 0) {
		return errors.New("invalid resource asset count/version")
	}
	seen := make(map[string]bool)
	for _, a := range r.Assets {
		if !resourceAssetPath(a.Path) || seen[a.Path] {
			return errors.New("invalid/duplicate resource asset path")
		}
		seen[a.Path] = true
		for _, f := range []*resourceFileIdentity{a.File, a.Target, a.SourceFile} {
			if f != nil && f.Inode == 0 {
				return errors.New("invalid resource file identity")
			}
		}
		for _, m := range []*resourceMountIdentity{a.Namespace, a.Mount} {
			if m != nil && (!looksLikeInstanceID(m.BootID) || m.Namespace == 0) {
				return errors.New("invalid resource mount identity")
			}
		}
		if a.Kind != "veth" && a.Link != nil {
			return errors.New("unexpected resource link identity")
		}
		switch a.Kind {
		case "veth":
			if err := validateResourceLink(r, a); err != nil {
				return err
			}
		case "materialised", "clone":
			if a.Target != nil || a.Source != "" || a.SourceFile != nil || a.Namespace != nil || a.Mount != nil || a.ReadOnly || a.OriginalMode != 0 {
				return errors.New("invalid temporary resource asset")
			}
		case "bind":
			if !resourceAssetPath(a.Source) || a.SourceFile == nil || a.Namespace == nil || a.Namespace.MountID != 0 || a.OriginalMode > 0o777 {
				return errors.New("invalid bind resource asset")
			}
			if a.Mount != nil && (a.Mount.MountID == 0 || a.Mount.BootID != a.Namespace.BootID || a.Mount.Namespace != a.Namespace.Namespace || a.Target == nil || a.File == nil || *a.File != *a.SourceFile) {
				return errors.New("inconsistent bind checkpoint")
			}
			if (a.Mount == nil) != (a.File == nil) {
				return errors.New("partial bind checkpoint")
			}
		case "jail", "netns":
			if r.Version < 3 || a.Target != nil || a.Source != "" || a.SourceFile != nil || a.ReadOnly || a.OriginalMode != 0 {
				return errors.New("invalid placement resource asset")
			}
			if a.Namespace != nil && a.Namespace.MountID != 0 {
				return errors.New("invalid placement namespace context")
			}
			if a.Kind == "jail" && a.Mount != nil {
				return errors.New("invalid jail mount checkpoint")
			}
			if a.Kind == "jail" && filepath.Base(a.Path) != r.Lease.Instance && (filepath.Base(a.Path) != "root" || filepath.Base(filepath.Dir(a.Path)) != r.Lease.Instance) {
				return errors.New("jail path does not match lease identity")
			}
			if a.Kind == "netns" {
				if l := r.Lease; l.Networkless || a.Path != filepath.Join("/run/netns", l.Netns) || a.Namespace == nil {
					return errors.New("invalid network namespace intent")
				}
				if (a.Mount == nil) != (a.File == nil) || (a.Mount != nil && (a.Mount.MountID == 0 || a.Mount.BootID != a.Namespace.BootID || a.Mount.Namespace != a.Namespace.Namespace)) {
					return errors.New("partial network namespace checkpoint")
				}
			}
		default:
			return errors.New("unknown resource asset kind")
		}
	}
	return nil
}

func (j *ResourceJournal) checkpointBindTarget(instance, path string, file resourceFileIdentity) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.records[instance]
	if !ok {
		return errors.New("bind target checkpoint requires lease intent")
	}
	r.Assets = cloneResourceAssets(r.Assets)
	for i := range r.Assets {
		a := &r.Assets[i]
		if a.Path != path {
			continue
		}
		if a.Kind != "bind" || a.Target != nil || a.Mount != nil {
			return errors.New("invalid bind target checkpoint")
		}
		a.Target = &file
		if err := r.validate(); err != nil {
			return err
		}
		return j.persist(r)
	}
	return errors.New("bind target checkpoint requires asset intent")
}

func (j *ResourceJournal) addAsset(instance string, a resourceAsset) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.records[instance]
	if !ok {
		return errors.New("resource asset requires committed lease intent")
	}
	if r.Version < 2 {
		r.Version = 2
	}
	if r.Version < 3 && (a.Kind == "jail" || a.Kind == "netns") {
		r.Version = 3
	}
	if a.Kind == "veth" {
		r.Version = 4
	}
	r.Assets = append(cloneResourceAssets(r.Assets), a)
	if err := r.validate(); err != nil {
		return err
	}
	return j.persist(r)
}

func (j *ResourceJournal) checkpointAsset(instance, path string, file resourceFileIdentity, mount *resourceMountIdentity) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.records[instance]
	if !ok {
		return errors.New("resource checkpoint requires lease intent")
	}
	r.Assets = cloneResourceAssets(r.Assets)
	for i := range r.Assets {
		if r.Assets[i].Path != path {
			continue
		}
		if r.Assets[i].File != nil {
			return errors.New("resource asset already checkpointed")
		}
		r.Assets[i].File, r.Assets[i].Mount = &file, mount
		if err := r.validate(); err != nil {
			return err
		}
		return j.persist(r)
	}
	return errors.New("resource checkpoint requires asset intent")
}

func (j *ResourceJournal) retireAsset(instance, path string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.records[instance]
	if !ok {
		return errors.New("resource retirement requires lease intent")
	}
	r.Assets = cloneResourceAssets(r.Assets)
	for i, a := range r.Assets {
		if a.Path == path {
			r.Assets = append(r.Assets[:i], r.Assets[i+1:]...)
			return j.persist(r)
		}
	}
	return j.directorySync()
}

func resourceFileID(info os.FileInfo) (resourceFileIdentity, error) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return resourceFileIdentity{}, errors.New("resource must be a regular file")
	}
	return resourceFileIdentity{Device: uint64(s.Dev), Inode: s.Ino}, nil
}

func (j *ResourceJournal) foreignBindReference(file resourceFileIdentity, owned map[string]bool) (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return false, errResourceJournalClosed
	}
	for _, r := range j.records {
		for _, a := range r.Assets {
			if a.Kind == "bind" && a.SourceFile != nil && *a.SourceFile == file && !owned[a.Path] {
				return true, nil
			}
		}
	}
	return false, nil
}

// A replaced path cannot authorize an unlink or permission restoration.
func resourceFileMatches(path string, expected resourceFileIdentity) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	actual, err := resourceFileID(info)
	if err != nil || actual != expected {
		return false, errors.New("resource file identity changed")
	}
	return true, nil
}

func (v *JailerVMM) newMaterialisedFile(instance, dir, pattern, kind string) (*os.File, error) {
	v.mu.Lock()
	journal := v.resourceJournal
	v.mu.Unlock()
	var f *os.File
	var err error
	if journal == nil {
		f, err = os.CreateTemp(dir, pattern)
	} else {
		if dir == "" {
			dir = os.TempDir()
		}
		dir, err = filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, strings.Replace(pattern, "*", rand.Text(), 1))
		if err = journal.addAsset(instance, resourceAsset{Kind: kind, Path: path}); err != nil {
			return nil, err
		}
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	}
	if err != nil {
		return nil, err
	}
	v.trackMaterialised(instance, f.Name()) // Before copy/reflink or a checkpoint can fail.
	v.mu.Lock()
	if v.materialisedIdentity == nil {
		v.materialisedIdentity = make(map[string]resourceFileIdentity)
	}
	v.materialisedIdentity[f.Name()] = resourceFileIdentity{} // Unknown identity must retain cleanup.
	v.mu.Unlock()
	info, err := f.Stat()
	var identity resourceFileIdentity
	if err == nil {
		identity, err = resourceFileID(info)
	}
	if err == nil {
		v.mu.Lock()
		if v.materialisedIdentity == nil {
			v.materialisedIdentity = make(map[string]resourceFileIdentity)
		}
		v.materialisedIdentity[f.Name()] = identity
		v.mu.Unlock()
		if journal != nil {
			err = journal.checkpointAsset(instance, f.Name(), identity, nil)
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("checkpoint staged file: %w", err)
	}
	return f, nil
}

func chmodResourceFile(path string, expected resourceFileIdentity, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	actual, err := resourceFileID(info)
	if err != nil || actual != expected {
		return errors.New("bind source identity changed")
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	return f.Sync()
}

func (v *JailerVMM) removeMaterialisedFile(path string) error {
	v.mu.Lock()
	identity, tracked := v.materialisedIdentity[path]
	v.mu.Unlock()
	if tracked {
		return removeResourceFile(path, identity)
	}
	return removeResourcePath(path)
}

func removeResourcePath(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func removeResourceFile(path string, identity resourceFileIdentity) error {
	present, err := resourceFileMatches(path, identity)
	if err != nil {
		return err
	}
	if present {
		if err := removeResourcePath(path); err != nil {
			return err
		}
	}
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(journalSyncDirectory(parent), parent.Close())
}

func (v *JailerVMM) retireResourceAsset(instance, path string) error {
	v.mu.Lock()
	journal := v.resourceJournal
	v.mu.Unlock()
	if journal == nil {
		return nil
	}
	return journal.retireAsset(instance, path)
}
