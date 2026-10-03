// adr: 399
package fcvm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

const resourceRecordLimit = 256 * 1024

var errResourceJournalClosed = errors.New("resource journal closed")

// ResourceJournal commits lease intent before guest resource creation and
// retires it only after the owning Manager confirms cleanup. Recovered records
// quarantine capacity; they do not authorize process adoption or deletion.
// No environment, command, credential or artifact contents are stored.
type ResourceJournal struct {
	mu            sync.Mutex
	dir           *os.Root
	lock          *os.File
	records       map[string]resourceJournalRecord
	closed        bool
	directorySync func() error
}

type resourceProcessIdentity struct {
	BootID     string `json:"boot_id"`
	PID        int    `json:"pid"`
	StartTicks uint64 `json:"start_ticks"`
}

type resourceJournalRecord struct {
	Version int                      `json:"version"`
	Lease   Lease                    `json:"lease"`
	Process *resourceProcessIdentity `json:"process,omitempty"`
	Assets  []resourceAsset          `json:"assets,omitempty"`
}

func resourceRecordName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:]) + ".json"
}

// CPU quota is mutable live policy, not a resource ownership identity.
func resourceLeaseMatches(a, b Lease) bool {
	b.CPUMillicores = a.CPUMillicores
	return a == b
}

// OpenResourceJournal locks private persistent storage. Corrupt, foreign,
// oversized or ambiguous records fail startup rather than free their slots.
func OpenResourceJournal(path string) (*ResourceJournal, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("resource journal: absolute persistent directory required")
	}
	// Ancestors are operator/systemd-provisioned. Creating only the leaf
	// means fsync of its existing parent covers the new directory entry.
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("resource journal: mkdir: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !journalOwned(info) {
		return nil, errors.New("resource journal: directory must be owner-only (0700)")
	}
	dir, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	j := &ResourceJournal{dir: dir, records: make(map[string]resourceJournalRecord)}
	f, err := dir.OpenFile(".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err == nil {
		var lockInfo os.FileInfo
		lockInfo, err = f.Stat()
		if err == nil {
			err = journalPrivateFile(lockInfo)
		}
		if err == nil {
			err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		}
	}
	if err != nil {
		if f != nil {
			_ = f.Close()
		}
		_ = dir.Close()
		return nil, fmt.Errorf("resource journal: exclusive lock: %w", err)
	}
	j.lock = f
	j.directorySync = func() error { return journalSyncDirectory(dir) }
	if err = j.load(); err == nil {
		err = j.directorySync()
	}
	if err == nil {
		var parent *os.Root
		parent, err = os.OpenRoot(filepath.Dir(path))
		if err == nil {
			err = errors.Join(journalSyncDirectory(parent), parent.Close())
		}
	}
	if err != nil {
		_ = j.Close()
		return nil, fmt.Errorf("resource journal: open: %w", err)
	}
	return j, nil
}

func journalOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func journalPrivateFile(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !journalOwned(info) || !ok || stat.Nlink != 1 {
		return errors.New("resource journal: record/lock must be a private regular file with one link (0600)")
	}
	return nil
}

func journalSyncDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func (j *ResourceJournal) load() error {
	entries, err := fs.ReadDir(j.dir.FS(), ".")
	if err != nil {
		return err
	}
	slots := make(map[int]bool)
	for _, entry := range entries {
		name := entry.Name()
		if name == ".lock" {
			continue
		}
		if strings.HasPrefix(name, ".pending-") && entry.Type().IsRegular() {
			if err := j.dir.Remove(name); err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".json") {
			return fmt.Errorf("unexpected journal entry %q", name)
		}
		r, err := j.readRecord(name)
		if err != nil {
			return err
		}
		if name != resourceRecordName(r.Lease.Instance) || slots[r.Lease.Slot] || len(j.records) >= MaxSlots {
			return errors.New("resource journal: ambiguous record identity or slot")
		}
		slots[r.Lease.Slot] = true
		j.records[r.Lease.Instance] = r
	}
	return nil
}

func (j *ResourceJournal) readRecord(name string) (resourceJournalRecord, error) {
	var r resourceJournalRecord
	f, err := j.dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return r, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return r, err
	}
	if err = journalPrivateFile(info); err != nil {
		return r, err
	}
	if info.Size() > resourceRecordLimit {
		return r, errors.New("oversized resource journal record")
	}
	d := json.NewDecoder(io.LimitReader(f, resourceRecordLimit+1))
	d.DisallowUnknownFields()
	if err = d.Decode(&r); err != nil {
		return r, err
	}
	if err = d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return r, errors.New("trailing resource journal data")
	}
	if r.Version == 1 && info.Size() > 8192 {
		return r, errors.New("oversized legacy resource journal record")
	}
	return r, r.validate()
}

func (r resourceJournalRecord) validate() error {
	l := r.Lease
	if (r.Version < 1 || r.Version > 4) || !restartResourceID(l.Instance) || len(l.Instance) > 64 || l.Slot < 0 || l.Slot >= MaxSlots || !l.Plan.Valid() || l.MemoryMaxMiB <= 0 {
		return errors.New("invalid resource journal lease/version")
	}
	want := leaseForSlot(l.Instance, l.Slot)
	if l.UID != want.UID || l.GID != want.GID || l.HostIP != want.HostIP || l.Netns != want.Netns || l.VethHost != want.VethHost || l.VethPeer != want.VethPeer {
		return errors.New("inconsistent resource journal slot identity")
	}
	if r.Process != nil && (r.Process.PID <= 1 || r.Process.StartTicks == 0 || !looksLikeInstanceID(r.Process.BootID)) {
		return errors.New("invalid resource journal process incarnation")
	}
	return r.validateAssets()
}

func (j *ResourceJournal) begin(l Lease) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r := resourceJournalRecord{Version: 3, Lease: l}
	if err := r.validate(); err != nil {
		return err
	}
	if _, ok := j.records[l.Instance]; ok {
		return errors.New("resource journal: instance already recorded")
	}
	for _, old := range j.records {
		if old.Lease.Slot == l.Slot {
			return errors.New("resource journal: slot already recorded")
		}
	}
	return j.persist(r)
}

func (j *ResourceJournal) recordProcess(l Lease, process resourceProcessIdentity) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.records[l.Instance]
	if !ok || !resourceLeaseMatches(r.Lease, l) {
		return errors.New("resource journal: process requires committed matching lease intent")
	}
	r.Process = &process
	if err := r.validate(); err != nil {
		return err
	}
	return j.persist(r)
}

// persist publishes the cache after rename, even if directory fsync fails.
// The caller still fails closed; cleanup can then retire that uncertain write.
func (j *ResourceJournal) persist(r resourceJournalRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > resourceRecordLimit {
		return errors.New("oversized resource journal record")
	}
	name := ".pending-" + rand.Text()
	f, err := j.dir.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = j.dir.Remove(name) }()
	n, err := f.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = j.dir.Rename(name, resourceRecordName(r.Lease.Instance)); err != nil {
		return err
	}
	j.records[r.Lease.Instance] = r
	return j.directorySync()
}

// forget is called after physical cleanup, before allocator release. An
// uncertain unlink keeps the in-memory record for a later fsync/retry.
func (j *ResourceJournal) forget(l Lease) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.records[l.Instance]
	if !ok {
		return nil
	} // Validation failures can precede journal intent.
	if !resourceLeaseMatches(r.Lease, l) {
		return errors.New("resource journal: cleanup lease does not match")
	}
	if err := j.dir.Remove(resourceRecordName(l.Instance)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := j.directorySync(); err != nil {
		return err
	}
	delete(j.records, l.Instance)
	return nil
}

func (j *ResourceJournal) snapshot() ([]resourceJournalRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil, errResourceJournalClosed
	}
	items := make([]resourceJournalRecord, 0, len(j.records))
	for _, r := range j.records {
		r.Assets = cloneResourceAssets(r.Assets)
		if r.Process != nil {
			p := *r.Process
			r.Process = &p
		}
		items = append(items, r)
	}
	return items, nil
}

func (j *ResourceJournal) lookup(instance string) (resourceJournalRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return resourceJournalRecord{}, false, errResourceJournalClosed
	}
	r, ok := j.records[instance]
	r.Assets = cloneResourceAssets(r.Assets)
	if r.Process != nil {
		p := *r.Process
		r.Process = &p
	}
	return r, ok, nil
}

func (j *ResourceJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return errors.Join(syscall.Flock(int(j.lock.Fd()), syscall.LOCK_UN), j.lock.Close(), j.dir.Close())
}

// WithResourceJournal is startup-only. Journaling is mandatory in Linux daemon
// wiring, optional for portable/injected Manager constructors.
func (m *Manager) WithResourceJournal(j *ResourceJournal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j == nil || m.resourceJournal != nil || m.restartInventoryDone || len(m.instanceFlights)+len(m.instanceStops)+len(m.live)+len(m.pendingCleanup) != 0 || m.preparedNetworks != nil || !m.alloc.pristine() {
		return errors.New("resource journal: attach before inventory and instance operations")
	}
	if _, err := j.snapshot(); err != nil {
		return err
	}
	m.resourceJournal = j
	if recorder, ok := m.vmm.(interface{ SetResourceJournal(*ResourceJournal) }); ok {
		recorder.SetResourceJournal(j)
	}
	return nil
}

func (m *Manager) journalLease(l Lease) error {
	if m.resourceJournal == nil {
		return nil
	}
	if err := m.resourceJournal.begin(l); err != nil {
		return fmt.Errorf("commit resource intent for %s: %w", l.Instance, err)
	}
	return nil
}
