package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	repairguest "github.com/onebox-faas/faas/pkg/datarepair/guest"
)

type dataRepairFlight struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type dataRepairRuntime struct {
	mu     sync.Mutex
	nodeID string
	runner core.RunnerSpec
	active map[string]*dataRepairFlight
}
type dataRepairRecord struct {
	Version          int                    `json:"version"`
	Frame            core.RehearsalFrame    `json:"frame"`
	Revoked          bool                   `json:"revoked"`
	RetiredAt        time.Time              `json:"retired_at"`
	NativeGeneration string                 `json:"native_generation"`
	NativeBootID     string                 `json:"native_boot_id"`
	NativeLease      Lease                  `json:"native_lease"`
	NativeProducer   nativeProducerIdentity `json:"native_producer,omitzero"`
}

type dataRepairContextKey struct{}

// Startup wiring, before serving. An empty runner disables creation while
// retaining cleanup for recorded attempts. Native process recovery is required.
func (m *Manager) WithDataRepairRehearsals(nodeID string, runner core.RunnerSpec) *Manager {
	m.dataRepair = &dataRepairRuntime{nodeID: nodeID, runner: runner, active: map[string]*dataRepairFlight{}}
	return m
}

func (m *Manager) DataRepairRehearsalIdentity() (nodeID, incarnation, runnerDigest string, err error) {
	if m == nil || m.dataRepair == nil || m.nativeVMM() == nil || m.dataRepair.runner.Validate() != nil {
		return "", "", "", core.ErrUnavailable
	}
	i, e := m.RuntimeAdmissionIdentity()
	if e != nil || i.NodeID != m.dataRepair.nodeID {
		return "", "", "", core.ErrUnavailable
	}
	return i.NodeID, i.Incarnation, m.dataRepair.runner.Digest(), nil
}

func (m *Manager) dataRepairPath(ctx context.Context, f core.RehearsalFrame) (string, error) {
	if m == nil || m.dataRepair == nil || m.nativeVMM() == nil || f.Validate() != nil || f.NodeID != m.dataRepair.nodeID || f.SnapshotSHA256 == "" {
		return "", core.ErrInvalid
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return "", core.ErrUnavailable
	}
	root := filepath.Join(m.nativeVMM().nativeRecoveryRuntime().journal.root, "data-repair")
	if os.MkdirAll(root, 0o700) != nil || checkNativeJournalPath(root, true) != nil {
		return "", core.ErrUnavailable
	}
	return filepath.Join(root, f.TaskID+".json"), nil
}

func readDataRepairRecord(path string) (dataRepairRecord, error) {
	var r dataRepairRecord
	file, err := os.Open(path) //nolint:forbidigo // Canonical task UUID under the owner-only native journal; inventory rejects non-regular entries.
	if err != nil {
		return r, err
	}
	defer func() { _ = file.Close() }()
	b, err := io.ReadAll(io.LimitReader(file, int64(api.DataRepairMaxRequestBytes+1)))
	if err != nil {
		return r, err
	}
	if len(b) > api.DataRepairMaxRequestBytes {
		return r, core.ErrConflict
	}
	keys := []string{"version", "frame", "revoked", "retired_at", "native_generation", "native_boot_id", "native_lease"}
	fields, shapeErr := nativeJournalObjectFields(b, keys)
	if shapeErr != nil {
		fields, shapeErr = nativeJournalObjectFields(b, append(keys, "native_producer"))
	}
	if shapeErr != nil || json.Unmarshal(b, &r) != nil || (r.Version != 1 && r.Version != 2) || r.Frame.Validate() != nil || r.Frame.SnapshotSHA256 == "" {
		return r, core.ErrConflict
	}
	_, producerPresent := fields["native_producer"]
	if producerPresent != (r.Version == 2) || r.Version == 2 && (r.NativeProducer.Protocol != nativeRepairProducerProtocol || r.NativeProducer.validate() != nil || r.NativeGeneration == "") {
		return r, core.ErrConflict
	}
	if r.NativeGeneration != "" && (!canonicalNativeHelperID(r.NativeGeneration) || !canonicalNativeHelperID(r.NativeBootID) || r.NativeLease.Instance != r.Frame.TaskID || !r.NativeLease.Networkless) {
		return r, core.ErrConflict
	}
	if r.NativeGeneration == "" && (r.NativeBootID != "" || r.NativeLease.Instance != "") || !r.RetiredAt.IsZero() && !r.Revoked {
		return r, core.ErrConflict
	}
	return r, nil
}

// Bind the incoming task to its physical generation before any native producer
// may start. Generic boot paths cannot borrow a rehearsal's instance identity.
func (j *nativeLaunchJournal) requireDataRepairProducer(ctx context.Context, instance string) error {
	if !validNativeInstanceName(instance) {
		return core.ErrInvalid
	}
	r, err := readDataRepairRecord(filepath.Join(j.root, "data-repair", instance+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	f, ok := ctx.Value(dataRepairContextKey{}).(core.RehearsalFrame)
	if err != nil || !ok || !r.Frame.Same(f) || r.Revoked || !r.RetiredAt.IsZero() || r.NativeGeneration != "" {
		return core.ErrConflict
	}
	return nil
}

func (j *nativeLaunchJournal) validateDataRepairRecords(ctx context.Context, physical []nativeLaunchRecord) error {
	root := filepath.Join(j.root, "data-repair")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		for _, p := range physical {
			if p.Producer != (nativeProducerIdentity{}) {
				return core.ErrConflict
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err = checkNativeJournalPath(root, true); err != nil {
		return err
	}
	byID := map[string]nativeLaunchRecord{}
	for _, p := range physical {
		byID[p.Lease.Instance] = p
	}
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".launch-") {
			continue
		}
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !canonicalNativeHelperID(id) || !entry.Type().IsRegular() {
			return core.ErrConflict
		}
		r, err := readDataRepairRecord(filepath.Join(root, entry.Name()))
		if err != nil || r.Frame.TaskID != id {
			return core.ErrConflict
		}
		p, found := byID[id]
		if r.NativeGeneration != "" && (!found || p.Generation != r.NativeGeneration || p.KernelBootID != r.NativeBootID || !sameNativePhysicalLease(p.Lease, r.NativeLease)) {
			return core.ErrConflict
		}
		if found && r.NativeGeneration != "" && p.Producer != r.NativeProducer {
			return core.ErrConflict
		}
		if found && (!p.Lease.Networkless || !r.RetiredAt.IsZero() && (!p.Revoked || !p.ExitConfirmed || !p.ResourcesRemoved)) {
			return core.ErrConflict
		}
		delete(byID, id)
	}
	for _, p := range byID {
		if p.Producer != (nativeProducerIdentity{}) {
			return core.ErrConflict
		}
	}
	return nil
}

func (m *Manager) prepareDataRepairNativeLease(ctx context.Context, lease Lease) (bool, error) {
	f, ok := ctx.Value(dataRepairContextKey{}).(core.RehearsalFrame)
	if !ok {
		return false, nil
	}
	if m.dataRepair == nil || m.nativeVMM() == nil {
		return true, core.ErrUnavailable
	}
	path := filepath.Join(m.nativeVMM().nativeRecoveryRuntime().journal.root, "data-repair", f.TaskID+".json")
	m.dataRepair.mu.Lock()
	defer m.dataRepair.mu.Unlock()
	r, err := readDataRepairRecord(path)
	if err != nil || !r.Frame.Same(f) || r.Revoked || r.NativeGeneration != "" || lease.Instance != f.TaskID || !lease.Networkless {
		return true, core.ErrConflict
	}
	if err = m.nativeVMM().prepareNativeLease(ctx, lease); err != nil {
		return true, err
	}
	physical, err := m.nativeVMM().nativeRecoveryRuntime().journal.read(f.TaskID)
	if err != nil {
		return true, err
	}
	r.NativeGeneration, r.NativeBootID, r.NativeLease = physical.Generation, physical.KernelBootID, physical.Lease
	if physical.Producer != (nativeProducerIdentity{}) {
		r.Version, r.NativeProducer = 2, physical.Producer
	}
	return true, writeNativeJournalValue(path, r)
}

func (m *Manager) verifyDataRepairImages(f core.RehearsalFrame) error {
	for _, image := range []struct{ key, hash string }{{m.paths.Kernel, f.Runner.KernelSHA256}, {f.Runner.BaseKey, f.Runner.BaseSHA256}, {f.Runner.LayerKey, f.Runner.LayerSHA256}} {
		path, err := m.resolveBackingFile(image.key)
		if err != nil {
			return core.ErrUnavailable
		}
		hash, err := m.fileDigest(path)
		if err != nil || hash != "sha256:"+image.hash {
			return core.ErrConflict
		}
		if image.key == f.Runner.LayerKey {
			info, err := os.Stat(path)
			if err != nil || info.Size() > api.DataRepairSandboxDiskBytes {
				return core.ErrConflict
			}
		}
	}
	return nil
}

// Run claims one immutable task before allocation and never replays it. The
// physical VM is networkless and has no app/deployment identity or secret files.
// Its result is discarded unless durable revocation and cleanup both succeed.
func (m *Manager) RunDataRepairRehearsal(parent context.Context, f core.RehearsalFrame, in core.RehearsalInput) (evidence core.Evidence, result error) {
	if m == nil {
		return evidence, core.ErrInvalid
	}
	// This producer has no ordinary app funding frame. Consult the original
	// app before native recovery, task claims or allocation can begin.
	if err := m.checkFinancialComputeBypass(parent, f.Scope.AppID); err != nil {
		return evidence, err
	}
	if err := m.rejectRehearsalExecution(parent, f.Scope.AppID); err != nil {
		return evidence, err
	}
	path, err := m.dataRepairPath(parent, f)
	if err != nil {
		return evidence, err
	}
	node, incarnation, runner, err := m.DataRepairRehearsalIdentity()
	if err != nil || node != f.NodeID || incarnation != f.Incarnation || runner != f.Runner.Digest() || !time.Now().Before(f.Deadline) || f.Deadline.After(time.Now().Add(time.Duration(api.DataRepairTimeoutSeconds)*time.Second)) || !f.Matches(in.Plan) || in.SnapshotSHA256 != f.SnapshotSHA256 || in.Validate() != nil {
		return evidence, core.ErrConflict
	}
	ctx, cancel := context.WithDeadline(parent, f.Deadline)
	defer cancel()
	rt := m.dataRepair
	rt.mu.Lock()
	if _, err = readDataRepairRecord(path); !errors.Is(err, os.ErrNotExist) {
		rt.mu.Unlock()
		return evidence, core.ErrConflict
	}
	// A rehearsal cannot borrow an ordinary VM or a recovered native identity.
	if _, err = m.nativeVMM().nativeRecoveryRuntime().journal.read(f.TaskID); !errors.Is(err, os.ErrNotExist) {
		rt.mu.Unlock()
		return evidence, core.ErrConflict
	}
	if err = writeNativeJournalValue(path, dataRepairRecord{Version: 1, Frame: f}); err != nil {
		rt.mu.Unlock()
		return evidence, core.ErrUnavailable
	}
	flight := &dataRepairFlight{cancel: cancel, done: make(chan struct{})}
	rt.active[f.TaskID] = flight
	rt.mu.Unlock()
	defer func() {
		if ctx.Err() != nil {
			result = core.ErrUnavailable
			evidence = core.Evidence{}
		}
		clean, stop := context.WithTimeout(context.WithoutCancel(parent), time.Duration(api.DataRepairRehearsalCleanupSeconds)*time.Second)
		defer stop()
		at, err := m.retireDataRepairOwned(clean, f, path)
		if err != nil {
			result = core.ErrUnavailable
			evidence = core.Evidence{}
		} else if result == nil {
			evidence.TaskID = f.TaskID
			evidence.NodeID = f.NodeID
			evidence.RunnerSHA256 = f.Runner.Digest()
			evidence.RetiredAt = &at
		}
		rt.mu.Lock()
		delete(rt.active, f.TaskID)
		close(flight.done)
		rt.mu.Unlock()
	}()
	if err = m.verifyDataRepairImages(f); err != nil {
		return evidence, err
	}
	ctx = context.WithValue(ctx, dataRepairContextKey{}, f)
	inst, err := m.Wake(ctx, WakeRequest{Instance: f.TaskID, ExecutionOnly: true, dataRepairOnly: true, AccountID: f.Scope.AccountID, BaseKey: f.Runner.BaseKey, LayerKey: f.Runner.LayerKey, Plan: f.Plan, VcpuCount: api.DataRepairSandboxVCPUs, MemSizeMiB: api.DataRepairSandboxMemoryMB, CPUMillicores: api.DataRepairSandboxCPUMillicores, DisableStartupCPUBoost: true})
	if err != nil {
		return evidence, core.ErrUnavailable
	}
	if inst == nil || !inst.ExecutionOnly || !inst.DataRepairOnly || !inst.Lease.Networkless {
		return evidence, core.ErrUnavailable
	}
	dialer, ok := m.vmm.(interface {
		DialDataRepairRehearsal(context.Context, Lease) (net.Conn, error)
	})
	if !ok {
		return evidence, core.ErrUnavailable
	}
	conn, err := dialer.DialDataRepairRehearsal(ctx, inst.Lease)
	if err != nil {
		return evidence, core.ErrUnavailable
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(f.Deadline)
	if err = core.WriteRehearsalInput(conn, in); err != nil {
		return evidence, core.ErrUnavailable
	}
	var guestResult repairguest.Result
	d := json.NewDecoder(io.LimitReader(conn, int64(api.DataRepairMaxRequestBytes)))
	d.DisallowUnknownFields()
	if d.Decode(&guestResult) != nil || guestResult.FailureCode != "" || guestResult.Evidence == nil {
		return evidence, core.ErrUnavailable
	}
	e := *guestResult.Evidence
	if !in.Plan.ValidCheckResults(e) || e.PlanDigest != f.PlanDigest || e.SnapshotSHA256 != f.SnapshotSHA256 || e.ChecksSHA256 != f.ChecksSHA256 || e.Runner != core.MicroVMRunner || e.RetiredAt != nil || e.TaskID != "" || e.NodeID != "" || e.RunnerSHA256 != "" {
		return evidence, core.ErrConflict
	}
	return e, nil
}

func (m *Manager) retireDataRepairOwned(ctx context.Context, f core.RehearsalFrame, path string) (time.Time, error) {
	rt := m.dataRepair
	rt.mu.Lock()
	r, err := readDataRepairRecord(path)
	if err != nil || !r.Frame.Same(f) {
		rt.mu.Unlock()
		return time.Time{}, core.ErrConflict
	}
	if !r.RetiredAt.IsZero() {
		rt.mu.Unlock()
		return r.RetiredAt, nil
	}
	r.Revoked = true
	err = writeNativeJournalValue(path, r)
	rt.mu.Unlock()
	if err != nil {
		return time.Time{}, core.ErrUnavailable
	}
	// Wake's native journal is written before any process/resource producer.
	// With its outer flight joined, absence here proves no native launch began.
	physical, err := m.nativeVMM().nativeRecoveryRuntime().journal.read(f.TaskID)
	if err == nil {
		if r.NativeGeneration != "" && (physical.Generation != r.NativeGeneration || physical.KernelBootID != r.NativeBootID || physical.Producer != r.NativeProducer || !sameNativePhysicalLease(physical.Lease, r.NativeLease)) {
			return time.Time{}, core.ErrConflict
		}
		if err = m.Destroy(ctx, f.TaskID); err != nil {
			return time.Time{}, core.ErrUnavailable
		}
		proof, proofErr := m.nativeVMM().nativeRecoveryRuntime().journal.read(f.TaskID)
		if proofErr != nil || proof.Generation != physical.Generation || !proof.Revoked || !proof.ExitConfirmed || !proof.ResourcesRemoved {
			return time.Time{}, core.ErrUnavailable
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return time.Time{}, core.ErrUnavailable
	} else if r.NativeGeneration != "" {
		return time.Time{}, core.ErrConflict
	} else if err = m.releaseUnstartedDataRepair(ctx, f.TaskID); err != nil {
		return time.Time{}, err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	r, err = readDataRepairRecord(path)
	if err != nil || !r.Frame.Same(f) || !r.Revoked {
		return time.Time{}, core.ErrConflict
	}
	r.RetiredAt = time.Now().UTC()
	if err = writeNativeJournalValue(path, r); err != nil {
		return time.Time{}, core.ErrUnavailable
	}
	return r.RetiredAt, nil
}

func (m *Manager) releaseUnstartedDataRepair(ctx context.Context, id string) error {
	stop, err := m.beginInstanceStop(ctx, id)
	if err != nil {
		return err
	}
	defer m.finishInstanceStop(id, stop)
	if err = m.cancelInFlightInstance(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.instanceFlights[id] != nil || m.live[id] != nil || m.qualificationInstances[id] != nil || len(m.nativeRecovered[id]) != 0 || m.exportDirs[id] != "" {
		return core.ErrConflict
	}
	if pending := m.pendingCleanup[id]; pending != nil && !pending.lease.Networkless {
		return core.ErrConflict
	}
	m.alloc.mu.Lock()
	defer m.alloc.mu.Unlock()
	if _, reserved := m.alloc.reserved[id]; reserved {
		return core.ErrConflict
	}
	for _, owner := range m.alloc.recovered {
		if owner == id {
			return core.ErrConflict
		}
	}
	if slot, leased := m.alloc.byInstance[id]; leased {
		delete(m.alloc.byInstance, id)
		m.alloc.free = append(m.alloc.free, slot)
		delete(m.cidToID, GuestVsockCID(slot))
	}
	delete(m.pendingCleanup, id)
	delete(m.waking, id)
	delete(m.pendingProcessExits, id)
	delete(m.processGenerations, id)
	delete(m.processStartedAt, id)
	return nil
}

func (m *Manager) RetireDataRepairRehearsal(ctx context.Context, f core.RehearsalFrame) (time.Time, error) {
	path, err := m.dataRepairPath(ctx, f)
	if err != nil {
		return time.Time{}, err
	}
	identity, identityErr := m.RuntimeAdmissionIdentity()
	if identityErr != nil {
		return time.Time{}, core.ErrUnavailable
	}
	rt := m.dataRepair
	rt.mu.Lock()
	r, err := readDataRepairRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		// Missing history from another daemon/host is not retirement proof.
		if identity.Incarnation != f.Incarnation {
			rt.mu.Unlock()
			return time.Time{}, core.ErrConflict
		}
		// Retire may precede a delayed Run. Never adopt another lifecycle's VM.
		if _, e := m.nativeVMM().nativeRecoveryRuntime().journal.read(f.TaskID); !errors.Is(e, os.ErrNotExist) {
			rt.mu.Unlock()
			return time.Time{}, core.ErrConflict
		}
		r = dataRepairRecord{Version: 1, Frame: f}
	} else if err != nil || !r.Frame.Same(f) {
		rt.mu.Unlock()
		return time.Time{}, core.ErrConflict
	}
	r.Revoked = true
	if err = writeNativeJournalValue(path, r); err != nil {
		rt.mu.Unlock()
		return time.Time{}, core.ErrUnavailable
	}
	flight := rt.active[f.TaskID]
	if flight != nil {
		flight.cancel()
	}
	rt.mu.Unlock()
	if flight != nil {
		select {
		case <-flight.done:
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		}
	}
	return m.retireDataRepairOwned(ctx, f, path)
}

func (v *JailerVMM) DialDataRepairRehearsal(ctx context.Context, l Lease) (net.Conn, error) {
	deadline := time.Now().Add(executionDialWindow)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for time.Now().Before(deadline) {
		conn, err := (&net.Dialer{Timeout: executionDialTimeout}).DialContext(ctx, "unix", v.vsockUDSSock(l.Instance))
		if err == nil {
			_ = conn.SetDeadline(minTime(deadline, time.Now().Add(executionDialTimeout)))
			if writeExecutionConnect(conn, core.RehearsalVsockPort) == nil {
				if ack, err := readConnectAck(conn); err == nil && ack == "OK" {
					_ = conn.SetDeadline(time.Time{})
					return conn, nil
				}
			}
			_ = conn.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(executionDialStep):
		}
	}
	return nil, core.ErrUnavailable
}
