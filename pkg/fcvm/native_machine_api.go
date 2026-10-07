package fcvm

// adr: 640
// adr: 641
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

type nativeMachineDial func(context.Context, string, nativeLaunchRecord) (net.Conn, error)

type nativeLiveProcessHandle interface {
	nativeProcessHandle
	CheckLive() error
}

// A session retains one original task. Read-only inventory does not inherit
// its producer authority. Cached sockets/shapes cannot authorize later resume.
type nativeMachineAPI struct {
	v                *JailerVMM
	owner            nativeLaunchRecord
	handle           nativeLiveProcessHandle
	client           *http.Client
	closed           bool
	inventory        *nativeEffectPermit // read-only; never original producer authority
	executableSource *nativeExecutableRecord
	executableFile   *os.File
	executableStamp  snapshotInputFileStamp
	executable       *NativeRuntimeExecutable
}

func (v *JailerVMM) withNativeMachineAPI(ctx context.Context, lease Lease, consume func(*nativeMachineAPI) error) (err error) {
	return v.withNativeMachineSession(ctx, lease, nil, consume)
}

// Observation is allowed for a recovered or fenced live task, but only while
// the complete root-wide inventory barrier remains held. It grants no effects.
func (v *JailerVMM) withObservedNativeMachine(ctx context.Context, expected nativeLaunchRecord, consume func(*nativeMachineAPI) error) error {
	return v.withNativeMachineSession(ctx, expected.Lease, &expected, consume)
}

func (v *JailerVMM) withNativeMachineSession(ctx context.Context, lease Lease, expected *nativeLaunchRecord, consume func(*nativeMachineAPI) error) (err error) {
	r := v.nativeRecovery
	if r == nil || r.journal == nil || consume == nil {
		return errors.New("native machine: original producer is unavailable")
	}
	var inventory *nativeEffectPermit
	if expected != nil {
		inventory, _ = ctx.Value(nativeEffectContextKey{}).(*nativeEffectPermit)
		if err := v.checkNativeInventoryPermit(ctx, inventory); err != nil {
			return err
		}
	} else if p, _ := ctx.Value(nativeEffectContextKey{}).(*nativeEffectPermit); p != nil && p.inventory {
		return errNativeInventoryMutation
	}
	lock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	if !owner.Authorized || expected == nil && owner.Revoked || owner.ExitConfirmed || owner.ResourcesRemoved || !sameNativePhysicalLease(owner.Lease, lease) {
		return errors.New("native machine: original live physical lease is required")
	}
	if expected != nil && !sameNativeObservedOwner(owner, *expected) {
		return errors.New("native machine: inventory owner changed while waiting")
	}
	if expected == nil && owner.RegionalRuntime != nil && owner.RegionalResources == nil {
		return errors.New("native machine: historical regional owner has no original guest shape")
	}
	open := r.retirer.open
	if open == nil {
		open = openNativeProcess
	}
	handle, err := open(owner.PID)
	if err != nil {
		if handle != nil {
			err = errors.Join(err, handle.Close())
		}
		return fmt.Errorf("native machine: pin original task: %w", err)
	}
	if handle == nil {
		return errors.New("native machine: missing pinned task")
	}
	defer func() { err = errors.Join(err, handle.Close()) }()
	live, ok := handle.(nativeLiveProcessHandle)
	if !ok {
		return errors.New("native machine: pinned task lacks independent liveness observation")
	}
	session := &nativeMachineAPI{v: v, owner: owner, handle: live, inventory: inventory}
	defer func() {
		session.closed = true
		if session.executableFile != nil {
			err = errors.Join(err, session.executableFile.Close())
		}
	}()
	session.executableSource, err = r.journal.readExecutable(owner)
	if err != nil {
		return err
	}
	if session.executableSource == nil && inventory == nil {
		return errors.New("native machine: original executable provenance is unavailable")
	}
	if err := session.check(ctx, false); err != nil {
		return err
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: session.dial}
	session.client = &http.Client{
		Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer func() { session.closed = true; transport.CloseIdleConnections() }()
	return errors.Join(consume(session), ctx.Err())
}

func (s *nativeMachineAPI) check(ctx context.Context, requireFirecracker bool) error {
	if s.closed {
		return errors.New("native machine: expired session")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r := s.v.nativeRecovery
	r.mu.Lock()
	generation, daemonLock := r.owned[s.owner.Lease.Instance], r.daemonLock
	r.mu.Unlock()
	if daemonLock == nil || s.inventory == nil && generation != s.owner.Generation {
		return errors.New("native machine: original local producer authority is required")
	}
	if _, err := daemonLock.Stat(); err != nil {
		return err
	}
	if s.inventory != nil {
		if err := s.v.checkNativeInventoryPermit(ctx, s.inventory); err != nil {
			return err
		}
	}
	current, err := r.journal.read(s.owner.Lease.Instance)
	if err != nil {
		return err
	}
	if !sameNativeObservedOwner(current, s.owner) {
		return errors.New("native machine: original owner changed")
	}
	// Poll the pinned task, including zombie exit. Numeric PID existence and
	// signal 0 alone do not establish that a task is still executing.
	if err := s.handle.CheckLive(); err != nil {
		return fmt.Errorf("native machine: original task is not live: %w", err)
	}
	process, found, err := r.retirer.probe.process(s.owner.PID)
	if err != nil {
		return err
	}
	l := s.owner.Lease
	if !found || process.PID != s.owner.PID || process.StartTime != s.owner.StartTime || process.Instance != l.Instance || process.UID != l.UID || process.Plan != l.Plan || process.IsBuilder != l.IsBuilder {
		return errors.New("native machine: process incarnation differs from original owner")
	}
	if requireFirecracker && (process.Jailer || process.ObservedUID != l.UID) {
		return errNativeMachineStarting
	}
	if !process.Jailer && process.ObservedUID == l.UID {
		return s.checkExecutable(ctx)
	}
	return ctx.Err()
}

var errNativeMachineStarting = errors.New("native machine: original task has not exec'd Firecracker")

func (s *nativeMachineAPI) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	dial := s.v.nativeRecovery.machineDial
	if dial == nil {
		dial = dialNativeMachine
	}
	// Wait only for startup/absent sockets. Once a request is sent, an ambiguous
	// transport failure is terminal; snapshot load and resume are never replayed.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		err := s.check(ctx, true)
		if err == nil {
			var conn net.Conn
			conn, err = dial(ctx, s.v.socketPath(s.owner.Lease.Instance), s.owner)
			if err == nil {
				if conn == nil {
					return nil, errors.New("native machine: missing bound connection")
				}
				if err = s.check(ctx, true); err == nil {
					return conn, nil
				}
			}
			if conn != nil {
				err = errors.Join(err, conn.Close())
			}
		}
		if !errors.Is(err, errNativeMachineStarting) && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (s *nativeMachineAPI) request(ctx context.Context, method, path string, body any, status int) ([]byte, error) {
	if s.inventory != nil && (method != http.MethodGet || path != "/" && path != "/machine-config") {
		return nil, errNativeInventoryMutation
	}
	if err := s.check(ctx, false); err != nil {
		return nil, err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Protocol size bound, not a tenant quota. No unbounded peer response is read.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil {
		return nil, err
	}
	if len(raw) > 8192 {
		return nil, errors.New("native machine: oversized API response")
	}
	if resp.StatusCode != status {
		// Do not expose arbitrary peer body content in errors/logs.
		return nil, fmt.Errorf("native machine: %s %s returned HTTP %d", method, path, resp.StatusCode)
	}
	if err := s.check(ctx, true); err != nil {
		return nil, err
	}
	return raw, nil
}

func nativeMachineObject(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("native machine: expected API object")
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || fields[key] != nil {
			return nil, errors.New("native machine: duplicate API field")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("native machine: trailing API data")
	}
	return fields, nil
}

func nativeMachineField(fields map[string]json.RawMessage, key string, target any) error {
	raw, ok := fields[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("native machine: missing %s", key)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("native machine: invalid %s: %w", key, err)
	}
	return nil
}

func (s *nativeMachineAPI) state(ctx context.Context) (string, error) {
	state, _, err := s.stateVersion(ctx)
	return state, err
}

func (s *nativeMachineAPI) stateVersion(ctx context.Context) (string, string, error) {
	raw, err := s.request(ctx, http.MethodGet, "/", nil, http.StatusOK)
	if err != nil {
		return "", "", err
	}
	fields, err := nativeMachineObject(raw)
	if err != nil {
		return "", "", err
	}
	var id, app, version, state string
	if err := errors.Join(nativeMachineField(fields, "id", &id), nativeMachineField(fields, "app_name", &app), nativeMachineField(fields, "vmm_version", &version), nativeMachineField(fields, "state", &state)); err != nil {
		return "", "", err
	}
	if id != s.owner.Lease.Instance || app != "Firecracker" || version == "" || state != "Paused" && state != "Running" && state != "Not started" {
		return "", "", errors.New("native machine: unexpected instance information")
	}
	return state, version, nil
}

// Observe both sides of the shape read. A default pre-load machine config or
// a changing VM state cannot pass as a loaded/serving machine observation.
func (s *nativeMachineAPI) shape(ctx context.Context, expectedState string, expected *Machine) (Machine, error) {
	machine, err := s.readShape(ctx, expectedState)
	if err != nil {
		return machine, err
	}
	if machine.MemSizeMib != s.owner.Lease.MemoryMaxMiB {
		return machine, errors.New("native machine: actual guest RAM differs from original lease")
	}
	if resources := s.owner.RegionalResources; resources != nil && (machine.VcpuCount != resources.VCPU || machine.MemSizeMib != resources.RAMMB) {
		return machine, errors.New("native machine: actual guest shape differs from original regional footprint")
	}
	if expected != nil && machine != *expected {
		return machine, errors.New("native machine: actual guest shape differs from boot configuration")
	}
	return machine, nil
}

func (s *nativeMachineAPI) readShape(ctx context.Context, expectedState string) (Machine, error) {
	var machine Machine
	if expectedState != "Paused" && expectedState != "Running" {
		return machine, errors.New("native machine: loaded machine state is required")
	}
	before, err := s.state(ctx)
	if err != nil || before != expectedState {
		return machine, errors.Join(err, errors.New("native machine: unexpected VM state before shape read"))
	}
	raw, err := s.request(ctx, http.MethodGet, "/machine-config", nil, http.StatusOK)
	if err != nil {
		return machine, err
	}
	fields, err := nativeMachineObject(raw)
	if err != nil {
		return machine, err
	}
	if err := errors.Join(nativeMachineField(fields, "vcpu_count", &machine.VcpuCount), nativeMachineField(fields, "mem_size_mib", &machine.MemSizeMib), nativeMachineField(fields, "smt", &machine.Smt)); err != nil {
		return machine, err
	}
	if machine.VcpuCount <= 0 || machine.MemSizeMib <= 0 {
		return machine, errors.New("native machine: invalid actual guest dimensions")
	}
	after, err := s.state(ctx)
	if err != nil || after != before {
		return machine, errors.Join(err, errors.New("native machine: VM state changed during shape read"))
	}
	return machine, nil
}

func (v *JailerVMM) loadSnapshot(ctx context.Context, lease Lease, statePath, memPath string, keepPaused bool, inputs ...*SnapshotInputs) error {
	body := map[string]any{"snapshot_path": statePath, "mem_backend": map[string]any{"backend_type": "File", "backend_path": memPath}, "resume_vm": !keepPaused}
	if v.nativeRecovery == nil {
		return v.apiPut(ctx, lease.Instance, "/snapshot/load", body)
	}
	body["resume_vm"] = false
	return v.withNativeMachineAPI(ctx, lease, func(s *nativeMachineAPI) error {
		var expected *Machine
		if len(inputs) == 1 && inputs[0] != nil {
			if err := checkNativeBootDimensions(s.owner, inputs[0].Machine); err != nil {
				return err
			}
			expected = &inputs[0].Machine
			_, version, err := s.stateVersion(ctx)
			if err != nil || version != inputs[0].FCVersion {
				return errors.Join(err, ErrSnapshotBackingChanged)
			}
		}
		if _, err := s.request(ctx, http.MethodPut, "/snapshot/load", body, http.StatusNoContent); err != nil {
			return err
		}
		if _, err := s.shape(ctx, "Paused", expected); err != nil {
			return err
		}
		if keepPaused {
			return nil
		}
		return s.resume(ctx)
	})
}

func (s *nativeMachineAPI) resume(ctx context.Context) error {
	if s.inventory != nil {
		return errNativeInventoryMutation
	}
	// A caller may have waited since a previous read. Always observe again at
	// the effect boundary; a later warm-pool promotion also gets a new session.
	state, err := s.state(ctx)
	if err != nil || state != "Paused" && state != "Running" {
		return errors.Join(err, errors.New("native machine: VM is not resumable"))
	}
	if _, err := s.shape(ctx, state, nil); err != nil {
		return err
	}
	if state == "Running" {
		return nil // An observed running original VM, never an arbitrary 409.
	}
	if _, err := s.request(ctx, http.MethodPatch, "/vm", map[string]any{"state": "Resumed"}, http.StatusNoContent); err != nil {
		return err
	}
	_, err = s.shape(ctx, "Running", nil)
	return err
}

func sameNativeObservedOwner(a, b nativeLaunchRecord) bool {
	return a.Authorized == b.Authorized && a.Revoked == b.Revoked && a.ExitConfirmed == b.ExitConfirmed && a.ResourcesRemoved == b.ResourcesRemoved && a.Generation == b.Generation && a.KernelBootID == b.KernelBootID && a.PID == b.PID && a.StartTime == b.StartTime && sameNativePhysicalLease(a.Lease, b.Lease) && sameNativeRegionalBinding(a, b)
}

func (v *JailerVMM) checkNativeBootMachine(ctx context.Context, lease Lease, expected Machine) error {
	if v.nativeRecovery == nil {
		return nil
	}
	return v.withNativeMachineAPI(ctx, lease, func(s *nativeMachineAPI) error {
		_, err := s.shape(ctx, "Running", &expected)
		return err
	})
}
