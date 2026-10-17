package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

type nativeMigrationSource struct {
	Version      int                          `json:"version"`
	Binding      state.MigrationSourceBinding `json:"binding"`
	Snapshotting bool                         `json:"snapshotting"`
	Outcome      string                       `json:"outcome"`
	Proof        string                       `json:"proof"`
}

type migrationSourceJournal struct{ owner *nativeLaunchJournal }

func syncMigrationSourceDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func migrationSourceKey(s string) string {
	hash := sha256.Sum256([]byte(s))
	return hex.EncodeToString(hash[:])
}

func (j migrationSourceJournal) root(instance string) string {
	return filepath.Join(j.owner.root, "migration-sources", migrationSourceKey(instance))
}

func (j migrationSourceJournal) path(b state.MigrationSourceBinding) string {
	return filepath.Join(j.root(b.InstanceID), migrationSourceKey(b.LeaseToken)+".json")
}

func (r *nativeMigrationSource) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, nativeQualificationJSONFields(reflect.TypeOf(*r)))
	if err != nil {
		return err
	}
	var names []string
	t := reflect.TypeOf(r.Binding)
	for i := 0; i < t.NumField(); i++ {
		names = append(names, t.Field(i).Name)
	}
	if _, err := nativeJournalObjectFields(fields["binding"], names); err != nil {
		return err
	}
	type plain nativeMigrationSource
	return json.Unmarshal(data, (*plain)(r))
}

func (j migrationSourceJournal) read(path string) (out nativeMigrationSource, err error) {
	for _, dir := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path)} {
		if err := checkNativeJournalPath(dir, true); err != nil {
			return out, err
		}
	}
	if err := checkNativeJournalPath(path, false); err != nil {
		return out, err
	}
	f, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	d := json.NewDecoder(f)
	if err := d.Decode(&out); err != nil {
		return out, err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return out, errors.New("migration source: trailing journal data")
	}
	if out.Version != 1 || out.Binding.Validate() != nil || path != j.path(out.Binding) || out.Outcome != "" && out.Outcome != state.MigrationSourceResumed && out.Outcome != state.MigrationSourceRetired || (out.Outcome == "") != (out.Proof == "") {
		return out, errors.New("migration source: invalid journal")
	}
	if out.Outcome != "" && out.Proof != migrationSourceProof(out) {
		return out, errors.New("migration source: invalid retained receipt")
	}
	return out, nil
}

func (j migrationSourceJournal) write(r nativeMigrationSource) error {
	root := filepath.Join(j.owner.root, "migration-sources")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(root, true); err != nil {
		return err
	}
	if err := os.MkdirAll(j.root(r.Binding.InstanceID), 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(j.root(r.Binding.InstanceID), true); err != nil {
		return err
	}
	// Persist the new directory entries before acknowledging a binding/proof.
	if err := syncMigrationSourceDir(j.owner.root); err != nil {
		return err
	}
	if err := syncMigrationSourceDir(root); err != nil {
		return err
	}
	return writeNativeJournalValue(j.path(r.Binding), r)
}

func migrationSourcePhysicalBinding(r nativeLaunchRecord) string {
	data, _ := json.Marshal(struct {
		Generation, Boot string
		Lease            Lease
	}{r.Generation, r.KernelBootID, r.Lease})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func migrationSourcePhysicalMatches(b state.MigrationSourceBinding, r nativeLaunchRecord) bool {
	return r.Generation == b.NativeGeneration && r.Lease.Instance == b.InstanceID && migrationSourcePhysicalBinding(r) == b.PhysicalBinding
}

func (m *Manager) sourceMigrationJournal(node string) (migrationSourceJournal, error) {
	if !m.MigrationAdmissionAvailable(node) {
		return migrationSourceJournal{}, errors.New("migration source: durable local native owner unavailable")
	}
	return migrationSourceJournal{owner: m.nativeVMM().nativeRecoveryRuntime().journal}, nil
}

// BindMigrationSource persists the physical identity before Snapshot can pause
// it. A caller persists the returned binding in state before dispatching capture.
func (m *Manager) BindMigrationSource(ctx context.Context, b state.MigrationSourceBinding) (state.MigrationSourceBinding, error) {
	j, err := m.sourceMigrationJournal(b.SourceNodeID)
	if err != nil {
		return b, err
	}
	lock, err := migrationAdoptionJournal(j).lock(ctx, b.InstanceID)
	if err != nil {
		return b, err
	}
	defer func() { _ = lock.Close() }()
	if old, err := j.read(j.path(b)); err == nil {
		if old.Binding.InstanceID != b.InstanceID || old.Binding.SourceNodeID != b.SourceNodeID || old.Binding.SourceWakeID != b.SourceWakeID || old.Binding.LeaseToken != b.LeaseToken {
			return b, errors.New("migration source: original binding changed")
		}
		return old.Binding, j.write(old)
	} else if !errors.Is(err, os.ErrNotExist) {
		return b, err
	}
	physical, err := j.owner.read(b.InstanceID)
	if err != nil {
		return b, err
	}
	m.mu.Lock()
	inst := m.live[b.InstanceID]
	valid := inst != nil && inst.nativeGeneration == physical.Generation && sameNativeJournalLease(inst.Lease, physical.Lease)
	m.mu.Unlock()
	if !valid || physical.Revoked || physical.ExitConfirmed || physical.ResourcesRemoved {
		return b, errors.New("migration source: original live VM unavailable")
	}
	b.NativeGeneration, b.PhysicalBinding = physical.Generation, migrationSourcePhysicalBinding(physical)
	if err := b.Validate(); err != nil {
		return b, err
	}
	return b, j.write(nativeMigrationSource{Version: 1, Binding: b})
}

func (m *Manager) SnapshotMigrationSource(ctx context.Context, b state.MigrationSourceBinding, spec SnapshotSpec) (SnapshotInfo, error) {
	j, err := m.sourceMigrationJournal(b.SourceNodeID)
	if err != nil {
		return SnapshotInfo{}, err
	}
	lock, err := migrationAdoptionJournal(j).lock(ctx, b.InstanceID)
	if err != nil {
		return SnapshotInfo{}, err
	}
	defer func() { _ = lock.Close() }()
	r, err := j.read(j.path(b))
	if err != nil {
		return SnapshotInfo{}, err
	}
	physical, err := j.owner.read(b.InstanceID)
	if err != nil || r.Binding != b || r.Outcome != "" || r.Snapshotting || !migrationSourcePhysicalMatches(b, physical) || physical.Revoked {
		return SnapshotInfo{}, errors.Join(err, errors.New("migration source: capture is no longer authorized"))
	}
	r.Snapshotting = true
	if err := j.write(r); err != nil {
		return SnapshotInfo{}, err
	}
	spec.operatorGeneration = b.NativeGeneration
	return m.SnapshotKeepAlive(ctx, b.InstanceID, spec)
}

// FinishMigrationSource returns only retained proof for the original physical
// owner. The instance-name preparation lock spans inspection and all VM effects.
func (m *Manager) FinishMigrationSource(ctx context.Context, target state.MigrationSource, retire bool) (string, string, error) {
	if retire && target.Phase != state.MigrationSourceRetiring || !retire && target.Phase != state.MigrationSourceBound {
		return "", "", errors.New("migration source: physical action lacks current source authority")
	}
	b := target.MigrationSourceBinding
	j, err := m.sourceMigrationJournal(b.SourceNodeID)
	if err != nil {
		return "", "", err
	}
	lock, err := migrationAdoptionJournal(j).lock(ctx, b.InstanceID)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = lock.Close() }()
	r, err := j.read(j.path(b))
	if err != nil {
		return "", "", err
	}
	if r.Binding != b {
		return "", "", errors.New("migration source: original ownership differs")
	}
	if r.Outcome != "" && (!retire || r.Outcome != state.MigrationSourceResumed) {
		return r.Outcome, r.Proof, j.write(r)
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return "", "", err
	}
	physical, err := j.owner.read(b.InstanceID)
	if err != nil || !migrationSourcePhysicalMatches(b, physical) {
		return "", "", errors.Join(err, errors.New("migration source: original native frame is unavailable"))
	}
	if retire && !physical.ResourcesRemoved {
		if err := m.Destroy(ctx, b.InstanceID); err != nil {
			return "", "", err
		}
		physical, err = j.owner.read(b.InstanceID)
		if err != nil {
			return "", "", err
		}
	}
	if physical.Revoked && physical.ExitConfirmed && physical.ResourcesRemoved && migrationSourcePhysicalMatches(b, physical) {
		r.Outcome = state.MigrationSourceRetired
	} else if !retire && !physical.Revoked && !physical.ExitConfirmed && !physical.ResourcesRemoved {
		m.mu.Lock()
		inst := m.live[b.InstanceID]
		valid := inst != nil && inst.nativeGeneration == b.NativeGeneration && sameNativeJournalLease(inst.Lease, physical.Lease)
		m.mu.Unlock()
		if !valid {
			return "", "", errors.New("migration source: original VM cannot be resumed")
		}
		if r.Snapshotting {
			if err := m.ResumeVM(ctx, b.InstanceID); err != nil {
				return "", "", err
			}
		}
		r.Outcome = state.MigrationSourceResumed
	} else {
		return "", "", errors.New("migration source: physical retirement remains unproven")
	}
	r.Proof = migrationSourceProof(r)
	return r.Outcome, r.Proof, j.write(r)
}

func migrationSourceProof(r nativeMigrationSource) string {
	return fmt.Sprintf("native-source:%s:%s:%s:%s", r.Binding.LeaseToken, r.Binding.NativeGeneration, r.Binding.PhysicalBinding, r.Outcome)
}

// Called with the same preparation lock already held. Preserve the proof before
// any new native generation overwrites the original frame, including generic boot.
func (j *nativeLaunchJournal) retainMigrationSourceProofs(instance string) error {
	source := migrationSourceJournal{owner: j}
	entries, err := os.ReadDir(source.root(instance))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := checkNativeJournalPath(source.root(instance), true); err != nil {
		return err
	}
	for _, entry := range entries {
		// An interrupted atomic write cannot authorize a physical operation.
		// Its canonical predecessor and native frame remain the authority.
		if strings.HasPrefix(entry.Name(), ".launch-") {
			continue
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return errors.New("migration source: unexpected source journal entry")
		}
		r, err := source.read(filepath.Join(source.root(instance), entry.Name()))
		if err != nil {
			return err
		}
		if r.Outcome != state.MigrationSourceRetired {
			physical, err := j.read(instance)
			if err != nil || !migrationSourcePhysicalMatches(r.Binding, physical) || !physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
				return errors.Join(err, errors.New("migration source: original physical owner is unresolved"))
			}
			r.Outcome = state.MigrationSourceRetired
			r.Proof = migrationSourceProof(r)
		}
		if err := source.write(r); err != nil {
			return err
		}
	}
	return nil
}
