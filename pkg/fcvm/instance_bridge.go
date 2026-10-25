package fcvm

// The instance bridge's record in the adoption block (connection-survival
// ADR §2 "Journal and adoption", slice 1c).
//
// With bridge units on (vmmdgrpc, FAAS_INSTANCE_BRIDGE_UNIT=1) an instance's
// bridge is a systemd service that outlives vmmd. vmmd records the run it
// started in the instance's version-7 adoption block: the unit and its
// invocation, the main process (boot, pid, start ticks), its executable's
// inode and its uid. A successor that adopts the instance proves the running
// unit against that record before it keeps serving through it; anything
// that does not match is stopped and the instance gets a new bridge.
//
// Like every other field of the block, the record is written only with
// adoption records on. A reader that predates the field rejects a block
// carrying it, so bridge units may be switched on only once every node that
// could read this journal, a rollback target included, runs a reader.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// InstanceBridge is a bridge unit run as vmmd started or adopted it.
type InstanceBridge struct {
	Unit         string
	InvocationID string
	PID          int
	Socket       string
	Port         int
	AppProtocol  string
	// Netns is the instance's network namespace name (AdoptedInstanceBridge).
	Netns string
}

type resourceAdoptionBridge struct {
	Unit         string                  `json:"unit"`
	InvocationID string                  `json:"invocation_id"`
	Process      resourceProcessIdentity `json:"process"`
	Exe          resourceFileIdentity    `json:"exe"`
	UID          int                     `json:"uid"`
	Socket       string                  `json:"socket"`
	Port         int                     `json:"port"`
	AppProtocol  string                  `json:"app_protocol,omitempty"`
}

func (b *resourceAdoptionBridge) clone() *resourceAdoptionBridge {
	if b == nil {
		return nil
	}
	c := *b
	return &c
}

func (b *resourceAdoptionBridge) validate(l Lease) error {
	if b == nil {
		return nil
	}
	rest, ok := strings.CutPrefix(b.Unit, "faas-ibridge-"+l.Instance+"-")
	if !ok || !strings.HasSuffix(rest, ".service") || !adoptionString(b.Unit) {
		return errors.New("adoption bridge unit does not name this instance")
	}
	if len(b.InvocationID) != 32 || strings.Trim(b.InvocationID, "0123456789abcdef") != "" {
		return errors.New("invalid adoption bridge invocation")
	}
	if b.Process.PID <= 1 || b.Process.StartTicks == 0 || !looksLikeInstanceID(b.Process.BootID) || b.Exe.Inode == 0 {
		return errors.New("invalid adoption bridge process identity")
	}
	if uid, ok := BridgeUIDForJailUID(l.UID); !ok || b.UID != uid {
		return errors.New("adoption bridge uid is not this lease's bridge uid")
	}
	if !filepath.IsAbs(b.Socket) || filepath.Clean(b.Socket) != b.Socket || !adoptionString(b.Socket) ||
		b.Port < 1 || b.Port > 65535 || !adoptionString(b.AppProtocol) {
		return errors.New("invalid adoption bridge endpoint")
	}
	return nil
}

// bridgeProcessIdentity reads the identity a bridge record pins: the main
// process, its executable and its uid (every uid of the process).
func bridgeProcessIdentity(procDir string, pid int) (resourceProcessIdentity, resourceFileIdentity, int, error) {
	proc, err := readResourceProcessIdentity(procDir, pid)
	if err != nil {
		return proc, resourceFileIdentity{}, 0, err
	}
	exe, err := adoptionExeIdentity(procDir, proc)
	if err != nil {
		return proc, exe, 0, err
	}
	status, err := os.ReadFile(filepath.Join(procDir, strconv.Itoa(pid), "status"))
	if err != nil {
		return proc, exe, 0, fmt.Errorf("read bridge process status: %w", err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		ids, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}
		fields := strings.Fields(ids)
		if len(fields) != 4 || fields[1] != fields[0] || fields[2] != fields[0] || fields[3] != fields[0] {
			return proc, exe, 0, fmt.Errorf("bridge process uids %q differ", strings.TrimSpace(ids))
		}
		uid, err := strconv.Atoi(fields[0])
		return proc, exe, uid, err
	}
	return proc, exe, 0, errors.New("bridge process status has no Uid line")
}

// RecordInstanceBridge records instance's bridge unit run in its adoption
// block. A ready instance's block is rewritten now; one that is still waking
// gets it with its first block. Without adoption records nothing adopts a
// bridge, and nothing is recorded.
func (m *Manager) RecordInstanceBridge(instance string, b InstanceBridge) error {
	w := m.adoption.Load()
	if w == nil {
		return nil
	}
	proc, exe, uid, err := bridgeProcessIdentity(w.procDir, b.PID)
	if err != nil {
		return fmt.Errorf("fcvm: record bridge %s: %w", b.Unit, err)
	}
	rec := &resourceAdoptionBridge{Unit: b.Unit, InvocationID: b.InvocationID, Process: proc, Exe: exe, UID: uid,
		Socket: b.Socket, Port: b.Port, AppProtocol: b.AppProtocol}
	w.commitMu.Lock()
	defer w.commitMu.Unlock()
	m.mu.Lock()
	inst := m.live[instance]
	if inst == nil {
		m.mu.Unlock()
		return fmt.Errorf("fcvm: record bridge %s: instance %s not live", b.Unit, instance)
	}
	if err := rec.validate(inst.Lease); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("fcvm: record bridge %s: %w", b.Unit, err)
	}
	inst.bridge = rec
	m.mu.Unlock()
	r, ok, err := m.resourceJournal.lookup(instance)
	if err != nil {
		return fmt.Errorf("fcvm: record bridge %s: %w", b.Unit, err)
	}
	if !ok || r.Adoption == nil {
		return nil // The first commit, at readiness, includes it.
	}
	if err := m.commitAdoptionLocked(instance, w); err != nil && !errors.Is(err, errAdoptionRecordStale) {
		return fmt.Errorf("fcvm: record bridge %s: %w", b.Unit, err)
	}
	return nil
}

// ForgetInstanceBridge drops the record of unit once vmmd has stopped it.
func (m *Manager) ForgetInstanceBridge(instance, unit string) {
	m.mu.Lock()
	inst := m.live[instance]
	changed := inst != nil && inst.bridge != nil && inst.bridge.Unit == unit
	if changed {
		inst.bridge = nil
	}
	m.mu.Unlock()
	if changed {
		m.markAdoptionDirty(instance)
	}
}

// AdoptedInstanceBridge returns the bridge unit run an adopted instance's
// record names: the one a successor may keep after proving it.
func (m *Manager) AdoptedInstanceBridge(instance string) (InstanceBridge, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	if inst == nil || inst.bridge == nil {
		return InstanceBridge{}, false
	}
	b := inst.bridge
	return InstanceBridge{Unit: b.Unit, InvocationID: b.InvocationID, PID: b.Process.PID, Socket: b.Socket,
		Port: b.Port, AppProtocol: b.AppProtocol, Netns: inst.Lease.Netns}, true
}

// ProveInstanceBridge checks that pid is still the recorded bridge process:
// the same boot, pid and start ticks, the same executable inode and the
// recorded bridge uid. systemd's view of the unit (invocation, main pid,
// cgroup) is the caller's to check.
func (m *Manager) ProveInstanceBridge(instance string, pid int) error {
	m.mu.Lock()
	inst := m.live[instance]
	var rec *resourceAdoptionBridge
	if inst != nil {
		rec = inst.bridge.clone()
	}
	m.mu.Unlock()
	if rec == nil {
		return fmt.Errorf("fcvm: instance %s has no recorded bridge", instance)
	}
	if pid != rec.Process.PID {
		return fmt.Errorf("fcvm: bridge %s runs pid %d, recorded %d", rec.Unit, pid, rec.Process.PID)
	}
	proc, exe, uid, err := bridgeProcessIdentity(m.bridgeProcDir(), pid)
	switch {
	case err != nil:
		return fmt.Errorf("fcvm: bridge %s: %w", rec.Unit, err)
	case proc != rec.Process:
		return fmt.Errorf("fcvm: bridge %s pid %d is another process", rec.Unit, pid)
	case exe != rec.Exe:
		return fmt.Errorf("fcvm: bridge %s runs another executable", rec.Unit)
	case uid != rec.UID:
		return fmt.Errorf("fcvm: bridge %s runs as uid %d, recorded %d", rec.Unit, uid, rec.UID)
	}
	return nil
}

func (m *Manager) bridgeProcDir() string {
	if w := m.adoption.Load(); w != nil && w.procDir != "" {
		return w.procDir
	}
	return "/proc"
}
