// migration_handlers.go — Tier A5 (ADR-066) four-phase
// cross-node live-instance migration handlers on the vmmd
// side.
//
// IMPORTANT — Phase 1 uses fcvm.SnapshotKeepAlive, not Park.
// The dying vmmd pauses the VM, writes the snapshot to canonical
// storage, and keeps the paused VM in its live map until Phase 4
// (resume) or Phase 5 (destroy). This makes the lease meaningful:
// a failed handoff can resume the original VM instead of leaving
// the instance with no process to serve traffic.
//
// The four phases map to the gRPC handlers as:
//
//   Phase 1  PrepareLiveMigration     — dying vmmd,
//                                       SnapshotKeepAlive
//                                       + lease mint + tracker put.
//   Phase 3  AdoptMigratedInstance    — new owner vmmd, lease
//                                       validate + restore the
//                                       snapshot through Wake.
//   Phase 4  CancelLiveMigration      — dying vmmd, lease delete.
//                                       resume the paused VM.
//   Phase 5  AcknowledgeMigration     — dying vmmd, lease delete.
//                                       destroy the paused source
//                                       VM. Idempotent with Phase 4
//                                       (a Phase 5 after a Phase 4
//                                       sees no lease and returns
//                                       success).
//
// The lease is the dying vmmd's authority. It is minted at
// Phase 1 and consulted only at Phase 3 (new owner proves
// the token before Restore). Phase 4 / 5 perform the
// corresponding source-VM action and then delete the tracker
// entry. On lease expiry the vmmd reconciles the durable
// instance row before resuming or destroying the source; an
// external cleanup error retains the entry for retry.

package vmmdgrpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// activeMigration is the in-memory tracker entry for a Phase 1
// lease. While pending is true the snapshot side effect has not
// completed yet; keeping that reservation before the side effect
// prevents concurrent Prepare calls from racing the same VM.
type activeMigration struct {
	instanceID     string
	leaseToken     string
	createdAt      time.Time
	leaseExpiresAt time.Time
	memKey         string
	vmstateKey     string
	pending        bool
}

// migrationTracker is the per-vmmd in-memory map of
// pending or completed-but-not-yet-acked-or-cancelled Phase 1
// results.
// The lease-expiry loop is the only background consumer.
type migrationTracker struct {
	mu    sync.Mutex
	state map[string]*activeMigration
}

func newMigrationTracker() *migrationTracker {
	return &migrationTracker{state: map[string]*activeMigration{}}
}

// put inserts a new entry. errAlreadyActive if the
// instance already has an active migration.
func (t *migrationTracker) put(m *activeMigration) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.state[m.instanceID]; ok {
		return errAlreadyActive{instanceID: m.instanceID}
	}
	t.state[m.instanceID] = m
	return nil
}

// reserve claims an instance before the snapshot side effect starts.
// complete fills in the storage metadata after SnapshotKeepAlive succeeds.
func (t *migrationTracker) reserve(instanceID string, createdAt time.Time) (*activeMigration, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.state[instanceID]; ok {
		return nil, errAlreadyActive{instanceID: instanceID}
	}
	m := &activeMigration{
		instanceID:     instanceID,
		leaseToken:     mintLeaseToken(),
		createdAt:      createdAt,
		leaseExpiresAt: createdAt.Add(time.Duration(api.MigrateLiveLeaseSeconds) * time.Second),
		pending:        true,
	}
	t.state[instanceID] = m
	return m, nil
}

func (t *migrationTracker) complete(leaseToken, memKey, vmstateKey string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range t.state {
		if m.leaseToken != leaseToken {
			continue
		}
		m.memKey = memKey
		m.vmstateKey = vmstateKey
		m.pending = false
		return nil
	}
	return errNoLease{}
}

// get fetches by instanceID + leaseToken. errNoLease on
// unknown instance, errLeaseMismatch on token mismatch.
func (t *migrationTracker) get(instanceID, leaseToken string) (*activeMigration, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.state[instanceID]
	if !ok {
		return nil, errNoLease{instanceID: instanceID}
	}
	if m.leaseToken != leaseToken {
		return nil, errLeaseMismatch{instanceID: instanceID}
	}
	return m, nil
}

// findByLeaseToken returns a copy of the entry held by leaseToken. Returning
// a copy keeps callers from retaining a pointer into the tracker while the
// expiry loop or an acknowledgement removes the entry.
func (t *migrationTracker) findByLeaseToken(leaseToken string) (activeMigration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range t.state {
		if m.leaseToken == leaseToken {
			return *m, true
		}
	}
	return activeMigration{}, false
}

// deleteByLeaseToken removes the entry only if leaseToken still owns it.
// Keeping the lookup and delete under one lock prevents an old Release from
// deleting a newer lease for the same instance after re-acquisition.
func (t *migrationTracker) deleteByLeaseToken(leaseToken string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for instanceID, m := range t.state {
		if m.leaseToken == leaseToken {
			delete(t.state, instanceID)
			return true
		}
	}
	return false
}

// delete removes an entry. Idempotent.
func (t *migrationTracker) delete(instanceID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.state, instanceID)
}

// listExpired returns the entries whose lease has expired
// (LeaseExpiresAt < now).
func (t *migrationTracker) listExpired(now time.Time) []*activeMigration {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*activeMigration
	for _, m := range t.state {
		if !m.pending && now.After(m.leaseExpiresAt) {
			copy := *m
			out = append(out, &copy)
		}
	}
	return out
}

// errAlreadyActive / errNoLease / errLeaseMismatch are
// tracker sentinel errors, mapped to gRPC codes by the
// handlers below.
type errAlreadyActive struct{ instanceID string }

func (e errAlreadyActive) Error() string {
	return "vmmd: migration already active for instance " + e.instanceID
}

type errNoLease struct{ instanceID string }

func (e errNoLease) Error() string {
	return "vmmd: no active migration for instance " + e.instanceID
}

type errLeaseMismatch struct{ instanceID string }

func (e errLeaseMismatch) Error() string {
	return "vmmd: lease token mismatch for instance " + e.instanceID
}

// mintLeaseToken mints a 128-bit hex token. crypto/rand is
// the source; uuid.NewString is the fallback if the OS RNG
// fails (degenerate but well-defined).
func mintLeaseToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uuid.NewString()
	}
	return hex.EncodeToString(b[:])
}

// These narrow optional seams keep VmmdAPI source-compatible with older
// test doubles while allowing the production Manager to expose the two
// migration-only operations that are not part of the ordinary VMM surface.
type migrationSnapshotter interface {
	SnapshotKeepAlive(context.Context, string, fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error)
}

type migrationResumer interface {
	ResumeVM(context.Context, string) error
}

type migrationInstanceReader interface {
	MigrationInstanceByID(context.Context, string) (state.Instance, error)
}

// PrepareLiveMigration — Phase 1.
//
// Wire errors:
//
//	codes.InvalidArgument   missing instanceID or snapshot_storage_key
//	codes.AlreadyExists     duplicate Phase 1 for this instance
//	codes.Unimplemented     vmmd does not expose the keep-alive snapshot and
//	                        resume seams required for a safe handoff
//	codes.Internal          snapshot failed (FC uAPI / storage); the paused
//	                        VM is resumed best-effort before returning.
func (s *Server) PrepareLiveMigration(ctx context.Context, req *vmmdpb.PrepareLiveMigrationRequest) (*vmmdpb.PrepareLiveMigrationResponse, error) {
	const op = "PrepareLiveMigration"
	start := time.Now()
	if req.GetInstanceId() == "" || req.GetSnapshotStorageKey() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing fields",
			"instance_id and snapshot_storage_key are required").WithDocs("https://" + wire.DocsHost + "/vmmd#prepare")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}

	memKey := req.GetSnapshotStorageKey()
	// vmstateKey is the canonical sibling under the same
	// namespace. Tier A5 uses "snap/<key-base>/{mem,vmstate}":
	// strip the trailing "/mem" and append "/vmstate".
	// Fallback "-vmstate" is defence-in-depth (the orchestrator
	// always emits the slash-suffixed form).
	vmstateKey := memKey
	if len(vmstateKey) >= 4 && vmstateKey[len(vmstateKey)-4:] == "/mem" {
		vmstateKey = vmstateKey[:len(vmstateKey)-4] + "/vmstate"
	} else {
		vmstateKey = vmstateKey + "-vmstate"
	}

	if s.migrations == nil {
		err := api.NewProblem(int(codes.Unavailable), "unavailable",
			"Migration unavailable", "migration lease tracking is not wired").WithDocs("https://" + wire.DocsHost + "/vmmd#prepare")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	snapshotter, ok := s.vmm.(migrationSnapshotter)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Migration unavailable", "vmmd does not support keep-alive snapshots").WithDocs("https://" + wire.DocsHost + "/vmmd#prepare")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	if _, ok := s.vmm.(migrationResumer); !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Migration unavailable", "vmmd does not support migration resume").WithDocs("https://" + wire.DocsHost + "/vmmd#prepare")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}

	// Reserve the instance before touching Firecracker. A second Prepare
	// must not snapshot the same VM while the first call is in flight.
	m, err := s.migrations.reserve(req.GetInstanceId(), start)
	if err != nil {
		err2 := api.NewProblem(int(codes.AlreadyExists), api.CodeConflict,
			"Migration already active", err.Error()).WithDocs("https://" + wire.DocsHost + "/vmmd#prepare")
		s.ops.Observe(op, time.Since(start), err2)
		return nil, grpcerr.ToStatus(err2)
	}

	_, err = snapshotter.SnapshotKeepAlive(ctx, req.GetInstanceId(), fcvm.SnapshotSpec{
		StageMemPath:      "",
		VMStatePath:       "",
		StorageKey:        memKey,
		VMStateStorageKey: vmstateKey,
	})
	if err != nil {
		s.migrations.deleteByLeaseToken(m.leaseToken)
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if err := s.migrations.complete(m.leaseToken, memKey, vmstateKey); err != nil {
		// A tracker race here means the snapshot succeeded but its lease
		// cannot be returned. Resume the source so it is never left paused.
		if resumer, ok := s.vmm.(migrationResumer); ok {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			resumeErr := resumer.ResumeVM(cleanupCtx, req.GetInstanceId())
			cancel()
			if resumeErr != nil {
				err = errors.Join(err, fmt.Errorf("resume after tracker failure: %w", resumeErr))
			}
		}
		s.migrations.deleteByLeaseToken(m.leaseToken)
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	s.ops.Observe(op, time.Since(start), nil)
	return &vmmdpb.PrepareLiveMigrationResponse{
		MemStorageKey:     memKey,
		VmstateStorageKey: vmstateKey,
		LeaseToken:        m.leaseToken,
		FcVersion:         s.fcVer,
	}, nil
}

func (s *Server) validateMigrationLease(ctx context.Context, instanceID, leaseToken string) error {
	if s.migrationStore != nil {
		reader, ok := s.migrationStore.(migrationInstanceReader)
		if !ok {
			return fmt.Errorf("vmmd: migration store has no migration-aware instance reader")
		}
		ins, err := reader.MigrationInstanceByID(ctx, instanceID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return errNoLease{instanceID: instanceID}
			}
			return fmt.Errorf("vmmd: migration lease lookup: %w", err)
		}
		if ins.LeaseToken != leaseToken {
			return errLeaseMismatch{instanceID: instanceID}
		}
		if !strings.EqualFold(ins.State, string(state.StateMigrating)) {
			return errNoLease{instanceID: instanceID}
		}
		return nil
	}
	if s.migrations == nil {
		return errNoLease{instanceID: instanceID}
	}
	_, err := s.migrations.get(instanceID, leaseToken)
	return err
}

func migrationLeaseProblem(err error, phase string) *api.Problem {
	code, apiCode := int(codes.Internal), "internal"
	var mismatch errLeaseMismatch
	var missing errNoLease
	switch {
	case errors.As(err, &mismatch):
		code, apiCode = int(codes.PermissionDenied), api.CodeUnauthorized
	case errors.As(err, &missing):
		code, apiCode = int(codes.NotFound), api.CodeNotFound
	}
	return api.NewProblem(code, apiCode, "Lease lookup failed", fmt.Sprintf("%s migration: %v", phase, err)).
		WithDocs("https://" + wire.DocsHost + "/vmmd#" + phase)
}

func (s *Server) destroyMigrationVM(ctx context.Context, instanceID string) error {
	if s == nil || s.vmm == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.vmm.Destroy(cleanupCtx, instanceID)
}

func (s *Server) resumeMigrationVM(ctx context.Context, instanceID string) (bool, error) {
	if s == nil || s.vmm == nil {
		return false, nil
	}
	resumer, ok := s.vmm.(migrationResumer)
	if !ok {
		return false, nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return true, resumer.ResumeVM(cleanupCtx, instanceID)
}

// AdoptMigratedInstance — Phase 3.
//
// The new owner validates the durable migration lease and restores the
// snapshot through the ordinary Manager.Wake path. The source vmmd owns the
// lease tracker, so destination validation is against the durable instance
// row rather than the destination's local in-memory tracker.
//
// Wire errors:
//
//	codes.InvalidArgument   missing required fields
//	codes.NotFound          lease is gone (Phase 4/5 ran, or
//	                        lease expired)
//	codes.PermissionDenied  lease token mismatch
func (s *Server) AdoptMigratedInstance(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	const op = "AdoptMigratedInstance"
	start := time.Now()
	if req.GetInstanceId() == "" || req.GetMemStorageKey() == "" ||
		req.GetVmstateStorageKey() == "" || req.GetLeaseToken() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing fields",
			"instance_id, mem_storage_key, vmstate_storage_key, and lease_token are required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#adopt")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	if err := s.validateMigrationLease(ctx, req.GetInstanceId(), req.GetLeaseToken()); err != nil {
		err2 := migrationLeaseProblem(err, "adopt")
		s.ops.Observe(op, time.Since(start), err2)
		return nil, grpcerr.ToStatus(err2)
	}

	// Keep nil-vmm legacy fixtures useful for lease/error-path tests. Every
	// production server has a VMM and takes the restore branch below.
	if s.vmm == nil {
		s.ops.Observe(op, time.Since(start), nil)
		return &vmmdpb.AdoptMigratedInstanceResponse{}, nil
	}
	wakeReq, err := toMigrationWakeRequest(ctx, req)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	inst, err := s.vmm.Wake(ctx, wakeReq)
	if err != nil {
		// Manager.Wake normally cleans its own partial allocation. The
		// explicit destroy is idempotent and also protects alternate VMM
		// implementations that return an error after registering a VM.
		if cleanupErr := s.destroyMigrationVM(ctx, req.GetInstanceId()); cleanupErr != nil && s.log != nil {
			s.log.Warn("vmmd: migration adoption cleanup failed",
				"instance_id", req.GetInstanceId(), "err", cleanupErr)
		}
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if inst == nil {
		err := fmt.Errorf("vmmd: migration adoption returned no instance")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	s.ops.Observe(op, time.Since(start), nil)
	return &vmmdpb.AdoptMigratedInstanceResponse{
		HostIp:   addrOrEmpty(inst.Lease.HostIP),
		Netns:    inst.Net.Netns,
		GuestUid: int32(inst.Lease.UID),
	}, nil
}

// AcknowledgeMigration — Phase 5.
//
// The new owner vmmd's schedd tells the dying vmmd "Phase 3
// committed; tear down the lease". The source VM is still paused
// from Phase 1, so destroy it before deleting the tracker entry.
// Idempotent on a stale Phase 5.
//
// Wire errors:
//
//	codes.InvalidArgument   missing fields
//	(all other errors absorbed: idempotent)
func (s *Server) AcknowledgeMigration(ctx context.Context, req *vmmdpb.AcknowledgeMigrationRequest) (*vmmdpb.AcknowledgeMigrationResponse, error) {
	const op = "AcknowledgeMigration"
	start := time.Now()
	if req.GetInstanceId() == "" || req.GetLeaseToken() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing fields",
			"instance_id and lease_token are required").WithDocs("https://" + wire.DocsHost + "/vmmd#ack")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	if s.migrations == nil {
		s.ops.Observe(op, time.Since(start), nil)
		return &vmmdpb.AcknowledgeMigrationResponse{}, nil
	}
	if _, getErr := s.migrations.get(req.GetInstanceId(), req.GetLeaseToken()); getErr != nil {
		// Stale ack — lease already cleared. Idempotent
		// success. The error is intentionally swallowed so
		// a peer that re-sends the ack on a stale lease
		// sees no failure (the lease is gone anyway).
		s.ops.Observe(op, time.Since(start), nil)
		return &vmmdpb.AcknowledgeMigrationResponse{}, nil //nolint:nilerr
	}
	if err := s.destroyMigrationVM(ctx, req.GetInstanceId()); err != nil {
		// Retain the lease so the expiry loop can retry source cleanup.
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	s.migrations.deleteByLeaseToken(req.GetLeaseToken())
	s.ops.Observe(op, time.Since(start), nil)
	return &vmmdpb.AcknowledgeMigrationResponse{}, nil
}

// CancelLiveMigration — Phase 4.
//
// The new owner vmmd's schedd tells the dying vmmd "abort —
// don't commit Phase 3". Resume the paused source VM, then
// delete the lease. The canonical snapshot stays in storage
// until the normal snapshot-drift sweep reaps it.
//
// Wire errors:
//
//	codes.InvalidArgument   missing fields
//	(all other errors absorbed: idempotent)
func (s *Server) CancelLiveMigration(ctx context.Context, req *vmmdpb.CancelLiveMigrationRequest) (*vmmdpb.CancelLiveMigrationResponse, error) {
	const op = "CancelLiveMigration"
	start := time.Now()
	if req.GetInstanceId() == "" || req.GetLeaseToken() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing fields",
			"instance_id and lease_token are required").WithDocs("https://" + wire.DocsHost + "/vmmd#cancel")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	if s.migrations == nil {
		s.ops.Observe(op, time.Since(start), nil)
		return &vmmdpb.CancelLiveMigrationResponse{}, nil
	}
	if _, getErr := s.migrations.get(req.GetInstanceId(), req.GetLeaseToken()); getErr != nil {
		// Stale cancel — idempotent success. The error is
		// intentionally swallowed: a re-sent cancel on a
		// stale lease is a no-op (the lease is gone).
		s.ops.Observe(op, time.Since(start), nil)
		return &vmmdpb.CancelLiveMigrationResponse{}, nil //nolint:nilerr
	}
	attempted, err := s.resumeMigrationVM(ctx, req.GetInstanceId())
	if err != nil {
		// Retain the lease so expiry can retry the resume rather than
		// dropping the only reference to a paused source VM.
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if s.vmm != nil && !attempted {
		// A live source must be resumed before the lease is removed. A
		// VmmdAPI that cannot do that is miswired; fail closed so expiry
		// can retry after the process is corrected.
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Migration unavailable", "vmmd does not support migration resume").WithDocs("https://" + wire.DocsHost + "/vmmd#cancel")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	s.migrations.deleteByLeaseToken(req.GetLeaseToken())
	s.ops.Observe(op, time.Since(start), nil)
	return &vmmdpb.CancelLiveMigrationResponse{}, nil
}

// cleanupExpiredMigration reconciles the durable row before choosing the
// source-side action. A committed row means the paused source must be
// destroyed; a still-migrating/rolled-back row means it must be resumed.
func (s *Server) cleanupExpiredMigration(ctx context.Context, m *activeMigration) error {
	if s.migrationStore != nil {
		reader, ok := s.migrationStore.(migrationInstanceReader)
		if !ok {
			return fmt.Errorf("vmmd: migration store has no migration-aware instance reader")
		}
		ins, err := reader.MigrationInstanceByID(ctx, m.instanceID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return s.destroyMigrationVM(ctx, m.instanceID)
			}
			return fmt.Errorf("vmmd: expired migration row lookup: %w", err)
		}
		stateName := strings.ToLower(strings.TrimSpace(ins.State))
		// A successful peer commit changes node_id before the source
		// lease is acknowledged. The local VM is then obsolete even if
		// the row is still RUNNING or the lease token differs because a
		// later migration has started. Destroy is safe and idempotent;
		// resuming would create a second serving VM.
		if s.nodeID != "" && ins.NodeID != "" && ins.NodeID != s.nodeID {
			return s.destroyMigrationVM(ctx, m.instanceID)
		}
		if stateName == string(state.StateRunning) && ins.LeaseToken == m.leaseToken {
			return s.destroyMigrationVM(ctx, m.instanceID)
		}
		if stateName == string(state.StateMigrating) && ins.LeaseToken == m.leaseToken {
			attempted, err := s.resumeMigrationVM(ctx, m.instanceID)
			if !attempted && err == nil && s.vmm != nil {
				return fmt.Errorf("vmmd: expired migration cannot resume source VM")
			}
			return err
		}
		// Phase 1 pauses the source before schedd's Phase 2 UPDATE.
		// During that short window the durable row is still RUNNING
		// with no lease token. Once the local node identity confirms
		// the row is still ours, resume the paused VM instead of
		// dropping the in-memory lease and stranding it.
		if stateName == string(state.StateRunning) && s.nodeID != "" && ins.NodeID == s.nodeID && ins.LeaseToken != m.leaseToken {
			attempted, err := s.resumeMigrationVM(ctx, m.instanceID)
			if !attempted && err == nil && s.vmm != nil {
				return fmt.Errorf("vmmd: expired pre-Phase-2 migration cannot resume source VM")
			}
			return err
		}
		// CancelInstanceMigration commits the durable rollback before the
		// wire cancel. A parked row with an empty lease therefore still
		// needs the source VM resumed. Other token/state combinations are
		// stale ownership and must not cause us to destroy a peer's VM.
		if strings.EqualFold(ins.State, string(state.StateParked)) && ins.LeaseToken == "" {
			attempted, err := s.resumeMigrationVM(ctx, m.instanceID)
			if !attempted && err == nil && s.vmm != nil {
				return fmt.Errorf("vmmd: expired rolled-back migration cannot resume source VM")
			}
			return err
		}
		// A durable terminal state means the scheduler no longer has a
		// serving instance to preserve. If this vmmd still owns the row,
		// remove the paused source VM as part of expiry cleanup.
		if stateName == string(state.StateStopped) || stateName == string(state.StateFailed) ||
			stateName == string(state.StateEvictingAccountDeleting) {
			return s.destroyMigrationVM(ctx, m.instanceID)
		}
		return nil
	}

	_, err := s.resumeMigrationVM(ctx, m.instanceID)
	return err
}

// LeaseExpiryLoop sweeps the migration tracker on a 5-second
// tick. An expired entry is removed only after its source VM
// cleanup succeeds; transient store/Firecracker errors leave the
// entry for a later retry.
//
// Started by cmd/vmmd's runWithDeps next to the cpuCache /
// netCache / activity goroutines. Exits on vmmd shutdown.
func (s *Server) LeaseExpiryLoop(ctx context.Context) {
	if s == nil || s.migrations == nil {
		return
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			entries := s.migrations.listExpired(now)
			for _, m := range entries {
				if err := s.cleanupExpiredMigration(ctx, m); err != nil {
					if s.log != nil {
						s.log.Warn("vmmd: migration lease cleanup failed",
							"instance_id", m.instanceID,
							"err", err,
						)
					}
					continue
				}
				if s.migrations.deleteByLeaseToken(m.leaseToken) && s.log != nil {
					s.log.Info("vmmd: migration lease expired",
						"instance_id", m.instanceID,
						"lease_seconds", int(time.Since(m.createdAt).Seconds()),
					)
				}
			}
		}
	}
}
