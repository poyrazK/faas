package fcvm

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/connreleaseproto"
)

// Connection release before destroy (ADR-20261109130000).
//
// Destroying a guest kills Firecracker; the guest kernel never sends a FIN or
// RST, and the netns conntrack entries go with the namespace. A database,
// cache or upstream the app was connected to keeps each session until its own
// TCP keepalive gives up, two hours on Linux defaults. Every idle park that
// reuses a retained snapshot (ADR-159) destroys the serving guest, so an app
// with a pool of ten connections that parks a few times an hour accumulates
// hundreds of orphaned server sessions and can exhaust max_connections, with
// any locks or LISTENs those sessions held still held.
//
// Before destroying an app guest that served traffic, vmmd asks guest-init to
// reset its off-guest TCP connections (SOCK_DESTROY sends an RST through the
// still-present NAT), then destroys it. The call is best-effort and bounded:
// a guest that predates it, a kernel without INET_DIAG_DESTROY or a timeout
// only logs, and the destroy goes ahead.

// releaseConnectionsWait bounds the host side of the call. guest-init stops at
// its own 400 ms budget and replies with what it reset.
const releaseConnectionsWait = 600 * time.Millisecond

// ReleaseGuestConnections asks guest-init to reset the guest's off-guest TCP
// connections and returns how many it reset.
func (v *JailerVMM) ReleaseGuestConnections(ctx context.Context, l Lease) (int, error) {
	if v == nil || v.chrootBase == "" || l.Instance == "" {
		return 0, fmt.Errorf("vmm: release connections: invalid VMM or instance")
	}
	callCtx, cancel := context.WithTimeout(ctx, releaseConnectionsWait)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(callCtx, "unix", v.vsockUDSSock(l.Instance))
	if err != nil {
		return 0, fmt.Errorf("vmm: release connections dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(callCtx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := callCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", resumeHookGuestPort); err != nil {
		return 0, fmt.Errorf("vmm: release connections CONNECT: %w", err)
	}
	if ack, err := readConnectAck(conn); err != nil || ack != "OK" {
		return 0, fmt.Errorf("vmm: release connections CONNECT reply %q: %w", ack, err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint32(msg[:4], connreleaseproto.MessageType)
	if _, err := conn.Write(msg[:]); err != nil {
		return 0, fmt.Errorf("vmm: release connections send: %w", err)
	}
	var reply [connreleaseproto.ReplyBytes]byte
	if _, err := io.ReadFull(conn, reply[:1]); err != nil {
		return 0, fmt.Errorf("vmm: release connections ack: %w", err)
	}
	if reply[0] != 0 {
		return 0, fmt.Errorf("vmm: release connections unsupported by guest (ack=%d)", reply[0])
	}
	if _, err := io.ReadFull(conn, reply[1:]); err != nil {
		return 0, fmt.Errorf("vmm: release connections count: %w", err)
	}
	return int(binary.BigEndian.Uint32(reply[1:])), nil
}

// connectionReleaser is the optional VMM capability teardown uses.
type connectionReleaser interface {
	ReleaseGuestConnections(context.Context, Lease) (int, error)
}

// WithReleaseConnectionsOnDestroy makes teardown reset an app guest's TCP
// connections before destroying it (ADR-20261109130000). Daemon startup
// configuration.
func (m *Manager) WithReleaseConnectionsOnDestroy(enabled bool) *Manager {
	m.releaseConnectionsOnDestroy = enabled
	return m
}

// releasesConnections reports whether teardown should reset this guest's
// connections: an app guest that may have served traffic. Builders, one-shot
// executions, repair and qualification VMs hold no customer sessions, and a
// warm-pool spare still holds only its capture's sockets, which point at
// sessions orphaned when the capture's VM was destroyed.
func (m *Manager) releasesConnections(inst *Instance, exportDir string) bool {
	if !m.releaseConnectionsOnDestroy || inst == nil || exportDir != "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !inst.Lease.IsBuilder && !inst.ExecutionOnly && !inst.DataRepairOnly &&
		!inst.QualificationOnly && !inst.Paused
}

// releaseConnectionsBeforeDestroy runs the best-effort reset. A quiet-paused
// or frozen guest is resumed first, since a paused guest cannot send; the
// destroy follows immediately, so neither resume serves anything.
func (m *Manager) releaseConnectionsBeforeDestroy(ctx context.Context, inst *Instance, exportDir string) {
	releaser, ok := m.vmm.(connectionReleaser)
	if !ok || !m.releasesConnections(inst, exportDir) {
		return
	}
	instance := inst.Lease.Instance
	if m.isFrozen(inst) {
		if err := m.vmm.ResumeVM(ctx, inst.Lease); err != nil {
			m.log.Debug("release connections: resume frozen guest", "instance", instance, "err", err)
			return
		}
	} else {
		m.quietResumeBestEffort(ctx, instance)
	}
	started := time.Now()
	n, err := releaser.ReleaseGuestConnections(ctx, inst.Lease)
	if err != nil {
		m.log.Debug("release connections before destroy", "instance", instance, "err", err)
		return
	}
	if n > 0 {
		m.log.Info("released guest connections before destroy", "instance", instance, "reset", n,
			"ms", time.Since(started).Milliseconds())
	}
}

// Capture and restore (ADR-20261110160000).
//
// The guest that takes a snapshot (idle park with capture, deploy prime) is
// paused for the capture and destroyed by Park, so before this its sessions
// were orphaned once per capture, and every instance restored from the
// snapshot woke holding the same "open" sockets: the first use got a reset or
// hung on retransmits. Park now resets the capturing guest's off-guest
// connections just before the capture, and a restored guest resets any it
// inherited (a snapshot taken before this change, a warm-tier capture whose
// guest kept serving) before it is published. Both are best effort, under
// the same switch as the reset before destroy.

// releaseConnectionsBeforeCapture resets the capturing guest's connections.
func (m *Manager) releaseConnectionsBeforeCapture(ctx context.Context, inst *Instance) {
	releaser, ok := m.vmm.(connectionReleaser)
	if !ok || !m.releasesConnections(inst, "") {
		return
	}
	started := time.Now()
	n, err := releaser.ReleaseGuestConnections(ctx, inst.Lease)
	if err != nil {
		m.log.Debug("release connections before capture", "instance", inst.Lease.Instance, "err", err)
		return
	}
	if n > 0 {
		m.log.Info("released guest connections before capture", "instance", inst.Lease.Instance, "reset", n,
			"ms", time.Since(started).Milliseconds())
	}
}

// releaseInheritedConnections resets the connections a guest restored from a
// snapshot inherited. Their sessions belong to the capture's guest, and the
// restored network namespace has none of their state. skip covers guests
// that hold no customer sessions.
func (m *Manager) releaseInheritedConnections(ctx context.Context, l Lease, skip bool) {
	releaser, ok := m.vmm.(connectionReleaser)
	if !ok || skip || !m.releaseConnectionsOnDestroy {
		return
	}
	n, err := releaser.ReleaseGuestConnections(ctx, l)
	if err != nil {
		m.log.Debug("release inherited connections after restore", "instance", l.Instance, "err", err)
		return
	}
	if n > 0 {
		m.log.Info("released inherited guest connections after restore", "instance", l.Instance, "reset", n)
	}
}
