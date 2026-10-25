package vmmdgrpc

// Instance bridges as systemd units (connection-survival ADR §2, slice 1c).
//
// With FAAS_INSTANCE_BRIDGE_UNIT=1 an instance's persistent bridge is a
// transient service, faas-ibridge-<instance>-<gen>.service, that vmmd
// creates over D-Bus. PID 1 forks it, so it is never in vmmd's cgroup and
// needs no parent-death signal; systemd applies the sandbox, the netns, the
// bridge uid and the per-bridge limits (pkg/api InstanceBridge*). It runs in
// faas-tenant-ibridge.slice, under the tenant memory fence instead of
// vmmd's control-plane budget.
//
// The bridge lives as long as its instance: the wake starts it beside the
// boot or restore (the prewarm), the idle reaper leaves it, and park,
// destroy, a failed wake or a freeze stop it and wait until systemd has
// unloaded it, so a parked app keeps no process or cgroup (invariant 4).
//
// This slice still stops every bridge unit when vmmd shuts down, and sweeps
// any it finds at startup (a crash leaves them): it does not adopt them
// yet. Only namespace bridges with a bridge uid (FAAS_BRIDGE_UNPRIVILEGED=1)
// run as units; public-target bridges keep the process path.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bridgeproc"
	"github.com/onebox-faas/faas/pkg/sdunit"
)

const (
	instanceBridgeUnitEnv     = "FAAS_INSTANCE_BRIDGE_UNIT"
	instanceBridgeUnitPrefix  = "faas-ibridge-"
	instanceBridgeUnitPattern = instanceBridgeUnitPrefix + "*.service"
	instanceBridgeSlice       = "faas-tenant-ibridge.slice"
	// instanceBridgeStopTimeout bounds a unit stop: TimeoutStopSec, then
	// SIGKILL, then systemd's unload.
	instanceBridgeStopTimeout = streamBridgeShutdownTimeout + 5*time.Second
	// instanceBridgeStartTimeout bounds the D-Bus start of one unit; the
	// socket readiness wait follows it.
	instanceBridgeStartTimeout = 10 * time.Second
)

// netnsRunDir holds the named network namespace bindings (ip netns).
var netnsRunDir = "/run/netns"

// InstanceBridgeUnitsEnabled reports whether instance bridges run as
// systemd units. It needs the v2 persistent bridge.
func InstanceBridgeUnitsEnabled() bool {
	return os.Getenv(instanceBridgeUnitEnv) == "1" && currentStreamBridgeVersion() == "v2" && persistentStreamBridgeEnabled()
}

var unitInstanceRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// bridgeUnitName names a new run of an instance's bridge. The generation
// keeps a run whose stop is still settling from blocking the next start.
func bridgeUnitName(instance string) (string, error) {
	if !unitInstanceRe.MatchString(instance) {
		return "", fmt.Errorf("instance %q cannot name a unit", instance)
	}
	var gen [4]byte
	if _, err := rand.Read(gen[:]); err != nil {
		return "", fmt.Errorf("bridge unit generation: %w", err)
	}
	return instanceBridgeUnitPrefix + instance + "-" + hex.EncodeToString(gen[:]) + ".service", nil
}

// bridgeUnitInstance returns the instance a bridge unit name belongs to.
func bridgeUnitInstance(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, instanceBridgeUnitPrefix)
	if !ok {
		return "", false
	}
	rest, ok = strings.CutSuffix(rest, ".service")
	if !ok {
		return "", false
	}
	i := strings.LastIndexByte(rest, '-')
	if i <= 0 || len(rest)-i-1 != 8 || !unitInstanceRe.MatchString(rest[:i]) {
		return "", false
	}
	if _, err := hex.DecodeString(rest[i+1:]); err != nil {
		return "", false
	}
	return rest[:i], true
}

// bridgeUnit is the run of an instance's bridge that an entry uses.
type bridgeUnit struct {
	name string
	unit sdunit.Unit
	proc unitProcess
}

// unitProcess is the bridge unit's main process, held by a pidfd so its
// liveness check cannot hit a reused pid.
type unitProcess interface {
	alive() bool
	close()
}

// unitProcessOpener opens the unit's main process and proves it is the
// unit's (its cgroup) and in the instance's namespace.
type unitProcessOpener func(u sdunit.Unit, netnsPath string) (unitProcess, error)

// useUnit reports whether entry's bridge runs as a unit: a namespace bridge
// with a bridge uid, with units switched on.
func (m *streamBridgeManager) useUnit(entry *streamBridgeEntry) bool {
	return m.unitsEnabled() && entry.identity.unprivileged() && entry.target == nil && entry.netns != ""
}

// unitManager returns the systemd connection, opening it on first use and
// again after the bus dropped it.
func (m *streamBridgeManager) unitManager(ctx context.Context) (sdunit.Manager, error) {
	m.unitMu.Lock()
	defer m.unitMu.Unlock()
	if c, ok := m.units.(interface{ Connected() bool }); ok && !c.Connected() {
		_ = m.units.Close()
		m.units = nil
	}
	if m.units == nil {
		units, err := m.connectUnits(ctx)
		if err != nil {
			return nil, err
		}
		m.units = units
	}
	return m.units, nil
}

func connectSystemd(ctx context.Context) (sdunit.Manager, error) { return sdunit.Connect(ctx) }

// bridgeService is the unit definition for entry's bridge.
func bridgeService(entry *streamBridgeEntry, bridgePath, netnsPath, guestIP string, port uint32, deadline string, env []string) sdunit.Service {
	// No parent pid: the unit must not die with vmmd. systemd's User=
	// refuses a uid with no account, and bridge uids have none, so the unit
	// starts the bridge as root holding only CAP_SETUID and CAP_SETGID; the
	// bridge drops to its uid before it reads anything and then verifies
	// every uid and that it holds no capability (pkg/bridgeproc).
	uid := strconv.Itoa(entry.identity.uid)
	base := []string{bridgeEnvPATH, bridgeproc.DropToUIDEnv + "=" + uid, bridgeproc.ExpectUIDEnv + "=" + uid}
	return sdunit.Service{
		Description:             "onebox-faas instance bridge " + entry.instance,
		ExecStart:               []string{bridgePath, entry.socket, guestIP, strconv.FormatUint(uint64(port), 10), deadline},
		Environment:             append(base, env...),
		Capabilities:            sdunit.CapSetUID | sdunit.CapSetGID,
		NetworkNamespacePath:    netnsPath,
		Slice:                   instanceBridgeSlice,
		MemoryMaxBytes:          api.InstanceBridgeMemoryMaxMB << 20,
		TasksMax:                api.InstanceBridgeTasksMax,
		LimitNOFILE:             2*api.InstanceBridgeMaxConnections + 64,
		ReadWritePaths:          []string{filepath.Dir(entry.socket)},
		IPAddressAllow:          []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr(guestIP), 32)},
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET"},
		TimeoutStop:             streamBridgeShutdownTimeout,
	}
}

// startEntryUnit starts entry's bridge as a unit in the namespace bound at
// netnsRunDir/netnsName.
func (m *streamBridgeManager) startEntryUnit(ctx context.Context, entry *streamBridgeEntry, bridgePath, netnsName, guestIP string, port uint32, deadline string, env []string) error {
	if err := entry.identity.prepareSocketDir(entry.socket); err != nil {
		return fmt.Errorf("stream bridge socket: %w", err)
	}
	netnsPath := filepath.Join(netnsRunDir, netnsName)
	info, err := os.Lstat(netnsPath)
	if err != nil {
		return fmt.Errorf("instance bridge netns: %w", err)
	}
	if info.Mode()&(os.ModeSymlink|os.ModeDir) != 0 {
		return fmt.Errorf("instance bridge netns %s is not a namespace binding", netnsPath)
	}
	name, err := bridgeUnitName(entry.instance)
	if err != nil {
		return err
	}
	units, err := m.unitManager(ctx)
	if err != nil {
		return fmt.Errorf("instance bridge unit: %w", err)
	}
	startCtx, cancel := context.WithTimeout(ctx, instanceBridgeStartTimeout)
	defer cancel()
	u, err := units.StartService(startCtx, name, bridgeService(entry, bridgePath, netnsPath, guestIP, port, deadline, env))
	if err == nil {
		var proc unitProcess
		if proc, err = m.openUnitProcess(u, netnsPath); err == nil {
			entry.unit = &bridgeUnit{name: name, unit: u, proc: proc}
			return nil
		}
	}
	// A start cut short may still bring the unit up: never leave it.
	m.stopUnitName(context.WithoutCancel(ctx), name)
	return fmt.Errorf("instance bridge unit %s: %w", name, err)
}

func (m *streamBridgeManager) stopUnitName(ctx context.Context, name string) {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), instanceBridgeStopTimeout)
	defer cancel()
	units, err := m.unitManager(stopCtx)
	if err == nil {
		err = units.Stop(stopCtx, name)
	}
	if err != nil {
		m.log.Warn("vmmd: instance bridge unit stop failed", "unit", name, "err", err)
	}
}

// stopEntryUnit stops entry's unit and waits until it is unloaded.
func (m *streamBridgeManager) stopEntryUnit(ctx context.Context, entry *streamBridgeEntry) {
	u := entry.unit
	m.stopUnitName(ctx, u.name)
	u.proc.close()
}

// sweepUnits stops every bridge unit of an instance with no entry. At startup that is all
// of them: this slice never adopts a predecessor's bridge, and nothing
// routes to it. It returns how many it tried to stop.
func (m *streamBridgeManager) sweepUnits(ctx context.Context) (int, error) {
	units, err := m.unitManager(ctx)
	if err != nil {
		return 0, err
	}
	names, err := units.List(ctx, instanceBridgeUnitPattern)
	if err != nil {
		return 0, err
	}
	// An instance with an entry owns its units, a starting one included
	// (whose unit field is not published yet).
	owned := map[string]bool{}
	m.mu.Lock()
	for _, entry := range m.entries {
		owned[entry.instance] = true
	}
	m.mu.Unlock()
	var stale []string
	for _, name := range names {
		if instance, ok := bridgeUnitInstance(name); ok && !owned[instance] {
			stale = append(stale, name)
		}
	}
	var wg sync.WaitGroup
	for _, name := range stale {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.stopUnitName(ctx, name)
		}()
	}
	wg.Wait()
	m.removeStaleSocketDirs()
	return len(stale), nil
}

// removeStaleSocketDirs removes the bridge socket directories no entry
// uses. Each bridge run has its own directory, so a unit stopped at startup
// leaves one behind. Call it only before serving: every unprivileged
// namespace bridge then is either an entry or stopped.
func (m *streamBridgeManager) removeStaleSocketDirs() {
	keep := map[string]bool{}
	m.mu.Lock()
	for _, entry := range m.entries {
		keep[filepath.Dir(entry.socket)] = true
	}
	m.mu.Unlock()
	if ensureBridgeRunRoot() != nil {
		return
	}
	dirs, err := os.ReadDir(bridgeRunRoot)
	if err != nil {
		return
	}
	for _, d := range dirs {
		dir := filepath.Join(bridgeRunRoot, d.Name())
		if !keep[dir] {
			// A bridge uid owned it; RemoveAll does not follow its symlinks.
			_ = os.RemoveAll(dir)
		}
	}
}

// SweepInstanceBridgeUnits stops the bridge units a previous vmmd left; call
// it before serving. A no-op unless instance bridge units are switched on.
func (s *Server) SweepInstanceBridgeUnits(ctx context.Context) {
	if s == nil || s.streamBridges == nil || !InstanceBridgeUnitsEnabled() {
		return
	}
	n, err := s.streamBridges.sweepUnits(ctx)
	if err != nil {
		s.log.Warn("vmmd: instance bridge unit sweep failed", "err", err)
		return
	}
	if n > 0 {
		s.log.Info("vmmd: stopped instance bridge units left by a previous vmmd", "units", n)
	}
}

// sameNamespace reports whether the namespace binding at path is the one
// the process pid runs in.
func sameNamespace(path string, pid int) bool {
	var want, got syscall.Stat_t
	if syscall.Stat(path, &want) != nil || syscall.Stat(fmt.Sprintf("/proc/%d/ns/net", pid), &got) != nil {
		return false
	}
	return want.Dev == got.Dev && want.Ino == got.Ino
}
