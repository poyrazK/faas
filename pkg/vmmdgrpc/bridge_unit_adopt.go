package vmmdgrpc

// Bridge units across a vmmd restart (connection-survival ADR §2 "Journal
// and adoption", slice 1c).
//
// vmmd records each bridge unit run it starts in the instance's adoption
// block (fcvm.Manager.RecordInstanceBridge). On a planned restart that
// adopts running guests, the stopping vmmd leaves its bridge units running.
// The successor, before it serves, keeps a unit only when all of these hold:
//
//   - its instance was adopted with a record naming this unit;
//   - systemd runs it with the recorded invocation and main pid;
//   - that pid is the recorded process (boot, start ticks), runs the
//     recorded executable inode as the recorded bridge uid, sits in the
//     unit's cgroup and in the instance's network namespace;
//   - its socket is the expected path and answers as the bridge uid.
//
// It stops every other faas-ibridge unit. The ADR's quarantine is not used
// for a unit that fails the proof: only vmmd (root) creates faas-ibridge
// units, so stopping one cannot hit another owner's process, and refusing
// the instance's adoption would turn a bridge fault into a lost guest. The
// instance gets a new bridge on its next request instead. Streams through
// the old vmmd still end with it: the RPCs terminate there until P2.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// instanceBridgeJournal is the part of fcvm.Manager that records and proves
// bridge unit runs.
type instanceBridgeJournal interface {
	RecordInstanceBridge(instance string, b fcvm.InstanceBridge) error
	ForgetInstanceBridge(instance, unit string)
	AdoptedInstanceBridge(instance string) (fcvm.InstanceBridge, bool)
	ProveInstanceBridge(instance string, pid int) error
}

var _ instanceBridgeJournal = (*fcvm.Manager)(nil)

const bridgeProbeTimeout = 2 * time.Second

// WithInstanceBridgeHandoff makes shutdown leave bridge units running for a
// successor that adopts the running guests (vmmd.toml adopt_running_vms with
// adoption_record).
func (s *Server) WithInstanceBridgeHandoff(on bool) *Server {
	if s.streamBridges != nil {
		s.streamBridges.keepUnits = on
	}
	return s
}

// recordUnit journals a started unit run so a successor can adopt it. A
// bridge the wake's prewarm started is not live yet: the first lease after
// the wake records it (needsRecordLocked). A failed record only costs the bridge
// at the next restart.
func (m *streamBridgeManager) recordUnit(entry *streamBridgeEntry) {
	if m.journal == nil || entry.unit == nil {
		return
	}
	u := entry.unit
	err := m.journal.RecordInstanceBridge(entry.instance, fcvm.InstanceBridge{Unit: u.name, InvocationID: u.unit.InvocationID,
		PID: u.unit.MainPID, Socket: entry.socket, Port: int(entry.port), AppProtocol: u.appProtocol})
	switch {
	case err == nil:
		m.mu.Lock()
		u.recorded = true
		m.mu.Unlock()
	case errors.Is(err, fcvm.ErrInstanceNotLive):
	default:
		m.log.Warn("vmmd: instance bridge unit not recorded; a vmmd restart replaces it", "unit", u.name, "err", err)
	}
}

// needsRecordLocked reports whether a leased entry's unit run is not in
// the adoption record yet: the first lease of a prewarmed bridge comes after
// its instance is live. The caller holds m.mu.
func (m *streamBridgeManager) needsRecordLocked(entry *streamBridgeEntry) bool {
	return m.journal != nil && entry.unit != nil && !entry.unit.recorded
}

// releaseForHandoff lets go of a unit bridge at shutdown without stopping it.
func (m *streamBridgeManager) releaseForHandoff(entry *streamBridgeEntry) {
	if entry.transport != nil {
		entry.transport.CloseIdleConnections() //nolint:staticcheck // http2.Transport, as closeEntry
	}
	entry.unit.proc.close()
}

// transientUnitDir is where systemd keeps the files of transient units.
var transientUnitDir = "/run/systemd/transient"

// ReconcileInstanceBridgeUnits keeps the bridge units of adopted instances
// that pass the proof and stops every other faas-ibridge unit. Call it once,
// after adoption and before serving. With units switched off it stops every
// faas-ibridge unit: a predecessor that ran them may have left them for a
// hand-off, and no instance may keep a bridge past its life (invariant 4).
func (s *Server) ReconcileInstanceBridgeUnits(ctx context.Context) {
	if s == nil || s.streamBridges == nil {
		return
	}
	adopt := bridgeAdopter(s.adoptBridgeUnit)
	if !InstanceBridgeUnitsEnabled() {
		// Most nodes never ran a unit; skip the D-Bus connection for them.
		if left, _ := filepath.Glob(filepath.Join(transientUnitDir, instanceBridgeUnitPattern)); len(left) == 0 {
			return
		}
		adopt = nil
	}
	adopted, stopped, err := s.streamBridges.reconcileUnits(ctx, adopt)
	if err != nil {
		s.log.Warn("vmmd: instance bridge unit reconcile failed", "err", err)
		return
	}
	if adopted+stopped > 0 {
		s.log.Info("vmmd: instance bridge units reconciled", "adopted", adopted, "stopped", stopped)
	}
}

type bridgeAdopter func(ctx context.Context, unit, instance string) (*streamBridgeEntry, error)

// reconcileUnits adopts what adopt proves and stops the rest of the bridge
// units of instances that have no entry yet.
func (m *streamBridgeManager) reconcileUnits(ctx context.Context, adopt bridgeAdopter) (adopted, stopped int, err error) {
	units, err := m.unitManager(ctx)
	if err != nil {
		return 0, 0, err
	}
	names, err := units.List(ctx, instanceBridgeUnitPattern)
	if err != nil {
		return 0, 0, err
	}
	owned := map[string]bool{}
	m.mu.Lock()
	for _, entry := range m.entries {
		owned[entry.instance] = true
	}
	m.mu.Unlock()
	var stale []string
	for _, name := range names {
		instance, ok := bridgeUnitInstance(name)
		if !ok || owned[instance] {
			continue
		}
		if adopt != nil {
			entry, err := adopt(ctx, name, instance)
			if err == nil && m.install(entry) {
				owned[instance] = true
				adopted++
				m.log.Info("vmmd: adopted instance bridge unit", "unit", name, "instance", instance)
				continue
			}
			if err != nil {
				m.log.Warn("vmmd: instance bridge unit not adopted; stopping it", "unit", name, "instance", instance, "reason", err)
			}
		}
		stale = append(stale, name)
	}
	for _, name := range stale {
		m.stopUnitName(ctx, name)
	}
	m.removeStaleSocketDirs()
	return adopted, len(stale), nil
}

var bridgeSocketSeqRe = regexp.MustCompile(`^[0-9]+-[0-9]+$`)

// isInstanceBridgeSocket reports whether socket is one of instance's bridge
// sockets: each run has its own directory, <instance>-<pid>-<seq>, under
// bridgeRunRoot (streamBridgeSockPathForRequest).
func isInstanceBridgeSocket(socket, instance string) bool {
	dir := filepath.Dir(socket)
	seq, ok := strings.CutPrefix(filepath.Base(dir), instance+"-")
	return ok && filepath.Clean(socket) == socket && filepath.Base(socket) == bridgeSocketName &&
		filepath.Dir(dir) == bridgeRunRoot && bridgeSocketSeqRe.MatchString(seq)
}

// install publishes an adopted entry unless the instance has one already.
func (m *streamBridgeManager) install(entry *streamBridgeEntry) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.entries[entry.mapKey()] != nil {
		entry.unit.proc.close()
		if entry.transport != nil {
			entry.transport.CloseIdleConnections() //nolint:staticcheck // http2.Transport, as closeEntry
		}
		return false
	}
	m.entries[entry.mapKey()] = entry
	m.rememberLocked(entry, entry.unit.appProtocol)
	return true
}

// adoptBridgeUnit proves unit is the run instance's adoption record names
// and returns a ready entry for it.
func (s *Server) adoptBridgeUnit(ctx context.Context, unit, instance string) (*streamBridgeEntry, error) {
	m := s.streamBridges
	if m.journal == nil {
		return nil, errors.New("no adoption journal")
	}
	rec, ok := m.journal.AdoptedInstanceBridge(instance)
	if !ok {
		return nil, errors.New("instance not adopted with a recorded bridge")
	}
	if rec.Unit != unit {
		return nil, fmt.Errorf("the record names %s", rec.Unit)
	}
	id, err := s.bridgeIdentityFor(instance)
	if err != nil || !id.unprivileged() {
		return nil, fmt.Errorf("instance has no bridge uid: %w", errors.Join(err, errors.New("unprivileged bridges off")))
	}
	if !isInstanceBridgeSocket(rec.Socket, instance) {
		return nil, fmt.Errorf("recorded socket %s is not the instance's", rec.Socket)
	}
	units, err := m.unitManager(ctx)
	if err != nil {
		return nil, err
	}
	u, err := units.Unit(ctx, unit)
	if err != nil {
		return nil, err
	}
	if !u.Active() || u.InvocationID != rec.InvocationID || u.MainPID != rec.PID {
		return nil, fmt.Errorf("systemd runs %s as invocation %s pid %d (%s), recorded %s pid %d",
			unit, u.InvocationID, u.MainPID, u.ActiveState, rec.InvocationID, rec.PID)
	}
	if err := m.journal.ProveInstanceBridge(instance, u.MainPID); err != nil {
		return nil, err
	}
	proc, err := m.openUnitProcess(u, filepath.Join(netnsRunDir, rec.Netns))
	if err != nil {
		return nil, err
	}
	if err := m.probeSocket(rec.Socket, id); err != nil {
		proc.close()
		return nil, err
	}
	ready := make(chan struct{})
	close(ready)
	entry := &streamBridgeEntry{
		key:      streamBridgeEntryKey(instance, nil),
		instance: instance,
		netns:    rec.Netns,
		port:     uint32(rec.Port), //nolint:gosec // validated 1..65535 by the journal reader
		protocol: streamBridgeProtocol(&vmmdpb.ForwardHTTPRequestInit{AppProtocol: rec.AppProtocol}),
		identity: id,
		socket:   rec.Socket,
		unit:     &bridgeUnit{name: unit, unit: u, proc: proc, appProtocol: rec.AppProtocol, recorded: true},
		ready:    ready,
		lastUsed: m.now(),
	}
	if err := m.connectEntry(entry); err != nil {
		proc.close()
		return nil, err
	}
	return entry, nil
}

// probeSocket checks the bridge answers on socket as its bridge uid.
func probeBridgeSocket(socket string, id bridgeIdentity) error {
	conn, err := net.DialTimeout("unix", socket, bridgeProbeTimeout)
	if err != nil {
		return fmt.Errorf("bridge socket: %w", err)
	}
	defer func() { _ = conn.Close() }()
	return id.verifyBridgePeer(conn)
}
