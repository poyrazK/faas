// adr: 473
// adr: 477
// adr: 478
// adr: 479
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
	"syscall"
)

const resourceRecordLimit = 256 * 1024

var errResourceJournalClosed = errors.New("resource journal closed")

// ResourceJournal commits lease intent before guest resource creation and
// retires it after the owning Manager confirms cleanup, or after startup proves
// an unclaimed prepared reservation obsolete across a kernel boot (ADR-478).
// Other recovered records quarantine capacity. None authorize process adoption
// or physical deletion by a replacement Manager.
// No environment, command, credential or artifact contents are stored.
type ResourceJournal struct {
	mu      journalMutex
	dir     *os.Root
	lock    *os.File
	records map[string]resourceJournalRecord
	closed  bool
	// commitSync makes a record write durable. Persists whose assets cannot
	// outlive a host reboot pass durable=false and skip the directory fsync
	// (see persist); tests replace it to fail either kind of commit.
	commitSync func(durable bool) error
	timing     map[string]resourceJournalTiming // see resource_journal_timing.go
}

type resourceProcessIdentity struct {
	BootID     string `json:"boot_id"`
	PID        int    `json:"pid"`
	StartTicks uint64 `json:"start_ticks"`
}

type resourceJournalRecord struct {
	Version  int                      `json:"version"`
	Lease    Lease                    `json:"lease"`
	Process  *resourceProcessIdentity `json:"process,omitempty"`
	Assets   []resourceAsset          `json:"assets,omitempty"`
	Prepared *resourcePreparedNetwork `json:"prepared,omitempty"`
	// Adoption is the version-7 adoption block (resource_adoption.go).
	Adoption *resourceAdoption `json:"adoption,omitempty"`
}

func resourceRecordName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:]) + ".json"
}

// CPU quota is mutable live policy, not a resource ownership identity.
func resourceLeaseMatches(a, b Lease) bool {
	// Process generations fence callbacks within one Manager lifetime. They
	// are private, absent from JSON and cannot be durable resource identity.
	// Restart provenance uses the separately recorded PID/start ticks/boot ID.
	// profileStartedAt is per-attempt profiling metadata with the same
	// properties: beginProcessAttempt restamps it for the ADR-005 cold-boot
	// fallback, which made every fallback fail with "launch lease mismatch"
	// (production-us hunt #9, H9-83).
	b.processGeneration = a.processGeneration
	b.profileStartedAt = a.profileStartedAt
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
	j.commitSync = func(durable bool) error {
		if !durable {
			return nil
		}
		return journalSyncDirectory(dir)
	}
	if err = j.load(); err == nil {
		err = j.commitSync(true)
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
	identities := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if name == ".lock" || name == resourceEpochFile {
			continue // The epoch claim is read separately (resource_epoch.go).
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
		if name != resourceRecordName(r.storageIdentity()) || slots[r.Lease.Slot] || len(j.records) >= MaxSlots {
			return errors.New("resource journal: ambiguous record identity or slot")
		}
		for _, id := range []string{r.storageIdentity(), r.Lease.Instance, preparedTarget(r)} {
			if id == "" {
				continue
			}
			if identities[id] {
				return errors.New("resource journal: overlapping prepared identity")
			}
		}
		for _, id := range []string{r.storageIdentity(), r.Lease.Instance, preparedTarget(r)} {
			if id != "" {
				identities[id] = true
			}
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
	if (r.Version < 1 || r.Version > resourceAdoptionVersion) || !restartResourceID(l.Instance) || len(l.Instance) > 64 || l.Slot < 0 || l.Slot >= MaxSlots || (!r.preparedSpare() && (!l.Plan.Valid() || l.MemoryMaxMiB <= 0)) {
		return errors.New("invalid resource journal lease/version")
	}
	want := leaseForSlot(l.Instance, l.Slot)
	if l.UID != want.UID || l.GID != want.GID || l.HostIP != want.HostIP || l.Netns != want.Netns || l.VethHost != want.VethHost || l.VethPeer != want.VethPeer {
		return errors.New("inconsistent resource journal slot identity")
	}
	if r.Process != nil && (r.Process.PID <= 1 || r.Process.StartTicks == 0 || !looksLikeInstanceID(r.Process.BootID)) {
		return errors.New("invalid resource journal process incarnation")
	}
	if err := r.validatePrepared(); err != nil {
		return err
	}
	if err := r.validateAdoption(); err != nil {
		return err
	}
	return r.validateAssets()
}

func (j *ResourceJournal) begin(l Lease) error {
	return j.beginRecord(resourceJournalRecord{Version: 3, Lease: l})
}

func (j *ResourceJournal) beginRecord(r resourceJournalRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	if err := r.validate(); err != nil {
		return err
	}
	if _, ok := j.recordForInstance(r.Lease.Instance); ok {
		return errors.New("resource journal: instance already recorded")
	}
	for _, old := range j.records {
		if old.Lease.Slot == r.Lease.Slot {
			return errors.New("resource journal: slot already recorded")
		}
	}
	return j.persist(r, false)
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
	return j.persist(r, false)
}

// persist publishes the cache after rename, even if directory fsync fails.
// The caller still fails closed; cleanup can then retire that uncertain write.
//
// The record file is always fsynced before the rename, so the name never
// points at partial content. Only a durable persist also fsyncs the directory
// (ADR-20261024135625). A volatile one is visible to a restarted vmmd at once,
// because a daemon crash keeps the page cache; after a power loss it may come
// back as the record's previous complete version, or not at all for a new
// record, which is safe because every asset such a write adds is gone after a
// reboot too (mounts, tmpfs placeholders, namespaces, links, processes). Any
// write that names a reboot-surviving asset is durable, and so is the whole
// record it writes. On production-us these commits were ~60 fsyncs, ~85 ms,
// of every restore's staging.
func (j *ResourceJournal) persist(r resourceJournalRecord, durable bool) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > resourceRecordLimit {
		return errors.New("oversized resource journal record")
	}
	key := r.timingKey()
	j.noteCommit(key)
	return j.persistFile(resourceRecordName(r.storageIdentity()), b, key, durable, func() {
		if r.Prepared != nil {
			delete(j.records, r.Prepared.Source)
			if r.Prepared.Source != key {
				delete(j.timing, r.Prepared.Source) // the spare's own, off any wake's path
			}
		}
		j.records[r.Lease.Instance] = r
	})
}

// persistFile replaces one journal file: private temporary file, fsync,
// atomic rename, then, when durable, directory fsync. published runs once the
// rename has replaced the target and before the directory fsync, even when
// that fsync then fails. A non-empty key accounts the fsyncs to that record's
// commit (noteCommit first). The caller holds j.mu.
func (j *ResourceJournal) persistFile(target string, b []byte, key string, durable bool, published func()) error {
	synced := func(fn func() error) error {
		if key == "" {
			return fn()
		}
		return j.timedSync(key, fn)
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
		err = synced(f.Sync)
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = j.dir.Rename(name, target); err != nil {
		return err
	}
	if published != nil {
		published()
	}
	return synced(func() error { return j.commitSync(durable) })
}

// forget is called after physical cleanup, before allocator release. An
// uncertain unlink keeps the in-memory record for a later fsync/retry.
func (j *ResourceJournal) forget(l Lease) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.recordForInstance(l.Instance)
	if !ok {
		delete(j.timing, l.Instance) // a commit that failed before its record existed
		return nil
	} // Validation failures can precede journal intent.
	if !resourceLeaseMatches(r.Lease, l) && !pendingPreparedLeaseMatches(r, l) {
		return errors.New("resource journal: cleanup lease does not match")
	}
	return j.forgetRecord(r)
}

func (j *ResourceJournal) forgetRecord(r resourceJournalRecord) error {
	key := r.timingKey()
	j.noteCommit(key)
	if err := j.dir.Remove(resourceRecordName(r.storageIdentity())); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := j.timedSync(key, func() error { return j.commitSync(true) }); err != nil {
		return err
	}
	delete(j.records, r.Lease.Instance)
	delete(j.timing, key)
	delete(j.timing, r.Lease.Instance)
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
		if r.Prepared != nil {
			p := *r.Prepared
			r.Prepared = &p
		}
		if r.Process != nil {
			p := *r.Process
			r.Process = &p
		}
		r.Adoption = r.Adoption.clone()
		items = append(items, r)
	}
	return items, nil
}

// RequireEmptyForNativeRecovery permits a native owner to hold the legacy
// journal's exclusive lock without adopting its records. A surviving legacy
// lease must be retired by its original recovery path before changing modes.
func (j *ResourceJournal) RequireEmptyForNativeRecovery() error {
	records, err := j.snapshot()
	if err != nil {
		return err
	}
	if len(records) != 0 {
		return fmt.Errorf("resource journal: %d legacy lease records prevent native recovery", len(records))
	}
	return nil
}

func (j *ResourceJournal) lookup(instance string) (resourceJournalRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return resourceJournalRecord{}, false, errResourceJournalClosed
	}
	r, ok := j.recordForInstance(instance)
	if r.Prepared != nil {
		p := *r.Prepared
		r.Prepared = &p
	}
	r.Assets = cloneResourceAssets(r.Assets)
	if r.Process != nil {
		p := *r.Process
		r.Process = &p
	}
	r.Adoption = r.Adoption.clone()
	return r, ok, nil
}

// Owns reports whether the journal holds a record for instance. A recorded
// instance's resources belong to verified recovery and are never reclaimed by
// name (ADR-477); a sweep may consider only unrecorded instances (ADR-631).
// A nil journal owns nothing.
func (j *ResourceJournal) Owns(instance string) (bool, error) {
	if j == nil {
		return false, nil
	}
	_, ok, err := j.lookup(instance)
	return ok, err
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
	if m.nativeVMM() != nil {
		return errors.New("resource journal: native qualification recovery cannot be combined with restart quarantine until shared producer ownership is qualified")
	}
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
	if adopted, err := m.adoptPreparedJournal(l); adopted || err != nil {
		return err
	}
	if err := m.resourceJournal.begin(l); err != nil {
		return fmt.Errorf("commit resource intent for %s: %w", l.Instance, err)
	}
	return nil
}
