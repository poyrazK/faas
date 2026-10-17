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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type migrationAdoptionContextKey struct{}

func (m *Manager) MigrationAdmissionAvailable(node string) bool {
	return m.nativeVMM() != nil && node != "" && m.nativeQualificationNodeID == node
}

type nativeMigrationAdoption struct {
	Version          int                          `json:"version"`
	KernelBootID     string                       `json:"kernel_boot_id"`
	Plan             state.MigrationAdmissionPlan `json:"plan"`
	Started          bool                         `json:"started"`
	Revoked          bool                         `json:"revoked"`
	Cleaned          bool                         `json:"cleaned"`
	NativeGeneration string                       `json:"native_generation"`
	NativeLease      Lease                        `json:"native_lease"`
	CleanupProof     string                       `json:"cleanup_proof"`
}

type migrationAdoptionJournal struct{ owner *nativeLaunchJournal }

func (j migrationAdoptionJournal) root() string {
	return filepath.Join(j.owner.root, "migration-admissions")
}
func (j migrationAdoptionJournal) path(instance string) string {
	key := sha256.Sum256([]byte(instance))
	return filepath.Join(j.root(), hex.EncodeToString(key[:])+".json")
}

func (j migrationAdoptionJournal) lock(ctx context.Context, instance string) (*os.File, error) {
	if !validNativeInstanceName(instance) {
		return nil, errors.New("migration adoption: invalid instance identity")
	}
	if err := os.MkdirAll(j.root(), 0o700); err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(j.root(), true); err != nil {
		return nil, err
	}
	return lockNativeJournalFile(ctx, j.path(instance)+".lock")
}

func (r *nativeMigrationAdoption) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, nativeQualificationJSONFields(reflect.TypeOf(*r)))
	if err != nil {
		return err
	}
	var planFields []string
	p := reflect.TypeOf(r.Plan)
	for i := 0; i < p.NumField(); i++ {
		planFields = append(planFields, p.Field(i).Name)
	}
	if _, err := nativeJournalObjectFields(fields["plan"], planFields); err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["native_lease"], nativeJournalLeaseFields()); err != nil {
		return err
	}
	type plain nativeMigrationAdoption
	return json.Unmarshal(data, (*plain)(r))
}

func (j migrationAdoptionJournal) read(instance string) (out nativeMigrationAdoption, err error) {
	if err := checkNativeJournalPath(j.root(), true); err != nil {
		return out, err
	}
	path := j.path(instance)
	if err := checkNativeJournalPath(path, false); err != nil {
		return out, err
	}
	f, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	d := json.NewDecoder(f)
	if err = d.Decode(&out); err != nil {
		return out, err
	}
	if err = d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return out, errors.New("migration adoption: trailing journal data")
	}
	err = nil
	boot, err := j.owner.currentBootID()
	if err != nil {
		return out, err
	}
	if out.Version != 1 || out.KernelBootID != boot || out.Plan.InstanceID != instance || !nativeQualificationUUID(out.Plan.ID) || !nativeQualificationUUID(out.Plan.DestinationNodeID) || !nativeQualificationUUID(out.Plan.DestinationWakeID) || out.Plan.DestinationGeneration < 1 || len(out.Plan.RequestDigest) != 64 || !out.Started && !out.Revoked || out.Cleaned && (!out.Revoked || out.CleanupProof == "") || !out.Cleaned && out.CleanupProof != "" {
		return out, errors.New("migration adoption: invalid incoming journal")
	}
	if out.NativeGeneration != "" {
		if !canonicalNativeHelperID(out.NativeGeneration) || validateNativeJournalLease(out.NativeLease) != nil || out.NativeLease.Instance != instance || !out.Started {
			return out, errors.New("migration adoption: invalid physical binding")
		}
	} else if out.NativeLease != (Lease{}) {
		return out, errors.New("migration adoption: unbound physical lease")
	}
	return out, nil
}

func (j migrationAdoptionJournal) write(r nativeMigrationAdoption) error {
	return writeNativeJournalValue(j.path(r.Plan.InstanceID), r)
}

// Ordinary native preparation takes this lock too, so publishing a revoke
// cannot race an unregistered producer which already passed the boot lookup.
func (j *nativeLaunchJournal) lockMigrationPreparation(ctx context.Context, instance string) (*os.File, error) {
	incoming := migrationAdoptionJournal{owner: j}
	p, bound := ctx.Value(migrationAdoptionContextKey{}).(state.MigrationAdmissionPlan)
	var lock *os.File
	var err error
	if !bound {
		lock, err = incoming.lock(ctx, instance)
		if err != nil {
			return nil, err
		}
	}
	if err := j.retainMigrationSourceProofs(instance); err != nil {
		if lock != nil {
			_ = lock.Close()
		}
		return nil, err
	}
	r, err := incoming.read(instance)
	if errors.Is(err, os.ErrNotExist) && !bound {
		return lock, nil
	}
	if err == nil && !bound && !r.Cleaned && r.NativeGeneration != "" {
		physical, physicalErr := j.read(instance)
		if physicalErr == nil && physical.Generation == r.NativeGeneration && sameNativeJournalLease(physical.Lease, r.NativeLease) && physical.Revoked && physical.ExitConfirmed && physical.ResourcesRemoved {
			r.Revoked, r.Cleaned = true, true
			r.CleanupProof = migrationPhysicalCleanupProof(r)
		}
	}
	if err == nil && !bound && r.Cleaned {
		err = incoming.write(r)
	}
	if err == nil && (bound && p == r.Plan && r.Started && !r.Revoked || !bound && r.Cleaned) {
		return lock, nil
	}
	if lock != nil {
		_ = lock.Close()
	}
	return nil, errors.Join(err, errors.New("migration adoption: original incoming ownership is unavailable"))
}

// Binding is fsynced before the native launch frame is created or replaced.
func (j *nativeLaunchJournal) bindMigrationPhysical(ctx context.Context, lease Lease, generation string) (string, error) {
	p, bound := ctx.Value(migrationAdoptionContextKey{}).(state.MigrationAdmissionPlan)
	if !bound {
		return generation, nil
	}
	incoming := migrationAdoptionJournal{owner: j}
	r, err := incoming.read(lease.Instance)
	if err != nil {
		return "", err
	}
	if r.Plan != p || !r.Started || r.Revoked {
		return "", errors.New("migration adoption: incoming grant was revoked")
	}
	r.NativeGeneration, r.NativeLease = generation, lease
	if err := incoming.write(r); err != nil {
		return "", err
	}
	return generation, nil
}

func (m *Manager) migrationAdoptionJournal(p state.MigrationAdmissionPlan) (migrationAdoptionJournal, error) {
	v := m.nativeVMM()
	if v == nil || !m.MigrationAdmissionAvailable(p.DestinationNodeID) || !nativeQualificationUUID(p.ID) || !nativeQualificationUUID(p.InstanceID) {
		return migrationAdoptionJournal{}, errors.New("migration adoption: durable local native owner unavailable")
	}
	return migrationAdoptionJournal{owner: v.nativeRecoveryRuntime().journal}, nil
}

// AdoptMigrationAdmission never replays a started dispatch. The stable file
// lock spans all boot effects and is shared with retirement across daemon death.
func (m *Manager) AdoptMigrationAdmission(ctx context.Context, p state.MigrationAdmissionPlan, req WakeRequest) (*Instance, error) {
	j, err := m.migrationAdoptionJournal(p)
	if err != nil {
		return nil, err
	}
	lock, err := j.lock(ctx, p.InstanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if !migrationRequestFits(p, req) {
		return nil, errors.New("migration adoption: request differs from reservation")
	}
	r, err := j.read(p.InstanceID)
	if err == nil {
		if r.Plan == p {
			return nil, state.ErrMigrationAdoptionUnresolved
		}
		old, physicalErr := j.owner.read(p.InstanceID)
		if !r.Cleaned && (physicalErr != nil || old.Generation != r.NativeGeneration || !old.Revoked || !old.ExitConfirmed || !old.ResourcesRemoved) {
			return nil, errors.New("migration adoption: prior incoming owner remains unresolved")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return nil, err
	}
	if physical, err := j.owner.read(p.InstanceID); err == nil {
		if !physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
			return nil, errors.New("migration adoption: another physical owner exists")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	boot, err := j.owner.currentBootID()
	if err != nil {
		return nil, err
	}
	r = nativeMigrationAdoption{Version: 1, KernelBootID: boot, Plan: p, Started: true}
	if err := j.write(r); err != nil {
		return nil, err
	}
	inst, err := m.Wake(context.WithValue(ctx, migrationAdoptionContextKey{}, p), req)
	if err != nil {
		return nil, err
	}
	r, err = j.read(p.InstanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil || inst.nativeGeneration == "" || inst.nativeGeneration != r.NativeGeneration || !sameNativeJournalLease(inst.Lease, r.NativeLease) {
		return nil, errors.New("migration adoption: boot has no original physical binding")
	}
	return inst, nil
}

func migrationRequestFits(p state.MigrationAdmissionPlan, req WakeRequest) bool {
	ram, cpu := int64(req.MemSizeMiB)+api.PerVMOverheadMB, int64(max(req.CPUMillicores, api.DefaultAppCPUMillicores))
	for _, sc := range req.Sidecars {
		sideRAM := sc.RamMB
		if sideRAM <= 0 {
			sideRAM = req.MemSizeMiB
		}
		ram += int64(sideRAM)
		cpu += int64(max(sc.CPUMillicores, api.DefaultAppCPUMillicores))
	}
	return req.Instance == p.InstanceID && req.MemSizeMiB > 0 && req.VcpuCount > 0 && ram <= int64(p.RAMMB) && cpu <= int64(p.CPUMillicores) && req.VcpuCount <= p.VCPU
}

// RetireMigrationAdmission first closes delayed creation, then proves the
// original generation's complete resource removal. Missing live-map state is
// never sufficient. A retained cleanup receipt can be replayed after replacement.
func (m *Manager) RetireMigrationAdmission(ctx context.Context, p state.MigrationAdmissionPlan) (string, error) {
	j, err := m.migrationAdoptionJournal(p)
	if err != nil {
		return "", err
	}
	lock, err := j.lock(ctx, p.InstanceID)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Close() }()
	r, err := j.read(p.InstanceID)
	if errors.Is(err, os.ErrNotExist) {
		boot, err := j.owner.currentBootID()
		if err != nil {
			return "", err
		}
		r = nativeMigrationAdoption{Version: 1, KernelBootID: boot, Plan: p, Revoked: true}
	} else if err != nil {
		return "", err
	}
	if r.Plan != p {
		return "", errors.New("migration adoption: cleanup belongs to a different incoming owner")
	}
	if r.Cleaned {
		return r.CleanupProof, j.write(r)
	}
	r.Revoked = true
	if err := j.write(r); err != nil {
		return "", err
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return "", err
	}
	physical, physicalErr := j.owner.read(p.InstanceID)
	if r.NativeGeneration == "" {
		if physicalErr == nil && (!physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved) || physicalErr != nil && !errors.Is(physicalErr, os.ErrNotExist) {
			return "", errors.Join(physicalErr, errors.New("migration adoption: unbound physical ownership exists"))
		}
		m.mu.Lock()
		busy := m.live[p.InstanceID] != nil || m.instanceFlights[p.InstanceID] != nil || m.pendingCleanup[p.InstanceID] != nil || len(m.nativeRecovered[p.InstanceID]) != 0
		m.mu.Unlock()
		if busy {
			return "", errors.New("migration adoption: unbound local resources remain")
		}
	} else {
		if physicalErr != nil {
			return "", physicalErr
		}
		if physical.Generation != r.NativeGeneration || !sameNativeJournalLease(physical.Lease, r.NativeLease) {
			return "", errors.New("migration adoption: original physical generation changed")
		}
		if !physical.ResourcesRemoved {
			if err := m.Destroy(ctx, p.InstanceID); err != nil {
				return "", err
			}
			physical, physicalErr = j.owner.read(p.InstanceID)
		}
		if physicalErr != nil || physical.Generation != r.NativeGeneration || !physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
			return "", errors.Join(physicalErr, errors.New("migration adoption: physical retirement is unproven"))
		}
	}
	r.Cleaned = true
	r.CleanupProof = migrationPhysicalCleanupProof(r)
	if err := j.write(r); err != nil {
		return "", err
	}
	return r.CleanupProof, nil
}

func migrationPhysicalCleanupProof(r nativeMigrationAdoption) string {
	return fmt.Sprintf("native:%s:%s:%s:resources_removed", r.KernelBootID, r.Plan.ID, r.NativeGeneration)
}
