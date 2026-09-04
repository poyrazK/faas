package vmmdgrpc

// The v2 stream bridge is a long-lived HTTP/2 server. Keeping one bridge and
// one unix-socket transport per live instance removes a process fork, socket
// bind, readiness poll, and transport handshake from every invocation.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/netns"
	"golang.org/x/net/http2"
)

const (
	streamBridgeIdleTimeout     = 2 * time.Minute
	streamBridgeReapInterval    = 30 * time.Second
	streamBridgePersistentEnv   = "FAAS_STREAM_BRIDGE_PERSISTENT"
	streamBridgePersistentValue = "1"
)

type streamBridgeSpawnFunc func(context.Context, string, string, string, string, uint32, string, []string) (*exec.Cmd, *bytes.Buffer, error)

type streamBridgeEntry struct {
	instance  string
	netns     string
	socket    string
	cmd       *exec.Cmd
	stderr    *bytes.Buffer
	client    *http.Client
	transport *http2.Transport
	waitDone  chan struct{}
	waitMu    sync.Mutex
	waitErr   error

	ready    chan struct{}
	starting bool
	startErr error
	closed   bool
	active   int
	lastUsed time.Time
}

type streamBridgeLease struct {
	manager *streamBridgeManager
	entry   *streamBridgeEntry
	once    sync.Once
}

func (l *streamBridgeLease) release() {
	if l == nil || l.manager == nil || l.entry == nil {
		return
	}
	l.once.Do(func() { l.manager.release(l.entry) })
}

type streamBridgeManager struct {
	mu      sync.Mutex
	entries map[string]*streamBridgeEntry
	log     *slog.Logger

	idleTimeout  time.Duration
	reapInterval time.Duration
	now          func() time.Time
	spawn        streamBridgeSpawnFunc
	waitSocket   func(string, time.Duration) error
	newTransport func(string) *http2.Transport
	stop         func(context.Context, *exec.Cmd, *bytes.Buffer) error

	reaperOnce sync.Once
	reaperCtx  context.Context
	cancel     context.CancelFunc
	closed     bool
	starts     sync.WaitGroup
}

func newStreamBridgeManager(log *slog.Logger) *streamBridgeManager {
	if log == nil {
		log = slog.Default()
	}
	return &streamBridgeManager{
		entries:      make(map[string]*streamBridgeEntry),
		log:          log,
		idleTimeout:  streamBridgeIdleTimeout,
		reapInterval: streamBridgeReapInterval,
		now:          time.Now,
		spawn:        streamBridgeSpawn,
		waitSocket:   waitForUnixSock,
		newTransport: newStreamBridgeH2CTransport,
		stop:         stopStreamBridge,
	}
}

func (m *streamBridgeManager) acquire(ctx context.Context, req *vmmdpb.ForwardHTTPRequestInit, netnsName string) (*streamBridgeLease, error) {
	if m == nil {
		return nil, errors.New("stream bridge manager is nil")
	}
	// The reaper belongs to the VMMD server, not to this request. It is
	// canceled by Server.Close, so it intentionally outlives ctx.
	m.startReaper() //nolint:contextcheck // manager lifetime is longer than one RPC
	m.mu.Lock()
	// A persistent bridge is owned by this manager, not by the request that
	// happened to start it. Passing ctx here would make exec.CommandContext
	// kill the bridge as soon as that request's gRPC stream closes, leaving a
	// stale entry and socket for the next request. The reaper context is
	// canceled only by manager shutdown and therefore matches the child
	// lifetime exactly.
	bridgeCtx := m.reaperCtx
	m.mu.Unlock()
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("stream bridge manager is closed")
		}
		entry := m.entries[req.GetInstance()]
		if entry == nil {
			entry = &streamBridgeEntry{
				instance: req.GetInstance(),
				netns:    netnsName,
				socket:   streamBridgeSockPath(req.GetInstance()),
				ready:    make(chan struct{}),
				starting: true,
			}
			m.entries[entry.instance] = entry
			m.starts.Add(1)
			m.mu.Unlock()

			err := m.startEntry(bridgeCtx, entry, req) //nolint:contextcheck // persistent bridge follows manager lifetime, not RPC lifetime
			m.starts.Done()
			if err != nil {
				return nil, err
			}
			continue
		}
		if entry.starting {
			ready := entry.ready
			m.mu.Unlock()
			select {
			case <-ready:
				if entry.startErr != nil {
					return nil, entry.startErr
				}
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if entry.closed {
			m.mu.Unlock()
			continue
		}
		if entry.netns != netnsName {
			// An instance ID can be reused after park/destroy and wake. Never
			// send a new request through a bridge that is attached to the old
			// network namespace, even if its child has not exited yet.
			delete(m.entries, entry.instance)
			entry.closed = true
			m.mu.Unlock()
			m.closeEntry(context.WithoutCancel(ctx), entry)
			continue
		}
		if !streamBridgeProcessAlive(entry.cmd) {
			delete(m.entries, entry.instance)
			entry.closed = true
			m.mu.Unlock()
			m.closeEntry(context.WithoutCancel(ctx), entry)
			continue
		}
		entry.active++
		entry.lastUsed = m.now()
		m.mu.Unlock()
		return &streamBridgeLease{manager: m, entry: entry}, nil
	}
}

func (m *streamBridgeManager) startEntry(ctx context.Context, entry *streamBridgeEntry, req *vmmdpb.ForwardHTTPRequestInit) error {
	err := m.startEntryProcess(ctx, entry, req)
	m.mu.Lock()
	entry.startErr = err
	entry.starting = false
	if err != nil || m.closed {
		delete(m.entries, entry.instance)
		entry.closed = true
	}
	close(entry.ready)
	m.mu.Unlock()
	if err != nil || entry.closed {
		m.closeEntry(context.WithoutCancel(ctx), entry)
	}
	return err
}

func (m *streamBridgeManager) startEntryProcess(ctx context.Context, entry *streamBridgeEntry, req *vmmdpb.ForwardHTTPRequestInit) error {
	bridgePath, err := resolveStreamBridgePath()
	if err != nil {
		return fmt.Errorf("stream bridge path: %w", err)
	}
	if _, err := os.Stat(bridgePath); err != nil {
		return fmt.Errorf("stream bridge binary missing at %s: %w", bridgePath, err)
	}
	port := req.GetPort()
	if port == 0 {
		port = netns.AppPort
	}
	if err := os.Remove(entry.socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale stream bridge socket: %w", err)
	}
	deadline := m.now().Add(streamBridgeSessionDeadline).UTC().Format(time.RFC3339)
	cmd, stderr, err := m.spawn(ctx, bridgePath, entry.netns, entry.socket, netns.GuestIP, port, deadline, streamBridgeStaticEnv(req))
	if err != nil {
		return fmt.Errorf("stream bridge start: %w", err)
	}
	entry.cmd = cmd
	entry.stderr = stderr
	entry.waitDone = make(chan struct{})
	go m.watchEntry(entry, cmd)
	if err := m.waitSocket(entry.socket, streamBridgeSocketReadyTimeout); err != nil {
		return fmt.Errorf("stream bridge socket not ready: %w", err)
	}
	select {
	case <-entry.waitDone:
		return fmt.Errorf("stream bridge exited before becoming ready: %w", entry.waitError())
	default:
	}
	transport := m.newTransport(entry.socket)
	m.mu.Lock()
	if entry.closed {
		m.mu.Unlock()
		transport.CloseIdleConnections()
		return fmt.Errorf("stream bridge exited before becoming ready: %w", entry.waitError())
	}
	entry.transport = transport
	entry.client = &http.Client{Transport: transport}
	m.mu.Unlock()
	return nil
}

// watchEntry reaps a persistent bridge even when it exits without a manager
// shutdown. Without a waiter, a terminated child can remain a zombie and
// still pass kill(0), causing the manager to reuse a dead entry and socket.
func (m *streamBridgeManager) watchEntry(entry *streamBridgeEntry, cmd *exec.Cmd) {
	err := cmd.Wait()
	entry.waitMu.Lock()
	entry.waitErr = err
	close(entry.waitDone)
	entry.waitMu.Unlock()

	m.mu.Lock()
	current := m.entries[entry.instance] == entry && !entry.closed
	unexpected := current && !m.closed
	if current {
		delete(m.entries, entry.instance)
		entry.closed = true
	}
	transport := entry.transport
	socket := entry.socket
	m.mu.Unlock()
	if !current {
		return
	}
	if transport != nil {
		transport.CloseIdleConnections()
	}
	_ = os.Remove(socket)
	if unexpected && err != nil {
		m.log.Warn("vmmd: persistent stream bridge exited", "instance", entry.instance, "err", err)
	}
}

func (e *streamBridgeEntry) waitError() error {
	e.waitMu.Lock()
	defer e.waitMu.Unlock()
	return e.waitErr
}

func (m *streamBridgeManager) release(entry *streamBridgeEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry.active > 0 {
		entry.active--
	}
	entry.lastUsed = m.now()
}

func (m *streamBridgeManager) invalidate(lease *streamBridgeLease) {
	if m == nil || lease == nil || lease.entry == nil {
		return
	}
	entry := lease.entry
	m.mu.Lock()
	if current := m.entries[entry.instance]; current != entry {
		m.mu.Unlock()
		return
	}
	delete(m.entries, entry.instance)
	entry.closed = true
	m.mu.Unlock()
	m.closeEntry(context.Background(), entry)
}

func (m *streamBridgeManager) closeEntry(ctx context.Context, entry *streamBridgeEntry) {
	if entry == nil {
		return
	}
	if entry.transport != nil {
		entry.transport.CloseIdleConnections()
	}
	if entry.cmd != nil {
		var err error
		if entry.waitDone == nil {
			err = m.stop(ctx, entry.cmd, entry.stderr)
		} else {
			err = m.stopWatchedEntry(ctx, entry)
		}
		if err != nil {
			m.log.Warn("vmmd: persistent stream bridge cleanup failed", "instance", entry.instance, "err", err)
		}
	}
	_ = os.Remove(entry.socket)
}

func (m *streamBridgeManager) stopWatchedEntry(ctx context.Context, entry *streamBridgeEntry) error {
	select {
	case <-entry.waitDone:
		return nil
	default:
	}
	if entry.cmd.Process != nil {
		_ = entry.cmd.Process.Signal(syscall.SIGTERM)
	}
	timer := time.NewTimer(streamBridgeShutdownTimeout)
	defer timer.Stop()
	select {
	case <-entry.waitDone:
		return nil
	case <-ctx.Done():
		if entry.cmd.Process != nil {
			_ = entry.cmd.Process.Kill()
		}
		<-entry.waitDone
		return nil
	case <-timer.C:
		if entry.cmd.Process != nil {
			_ = entry.cmd.Process.Kill()
		}
		<-entry.waitDone
		return nil
	}
}

func (m *streamBridgeManager) startReaper() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.reaperOnce.Do(func() {
		m.reaperCtx, m.cancel = context.WithCancel(context.Background())
		go m.reapLoop()
	})
}

func (m *streamBridgeManager) reapLoop() {
	ticker := time.NewTicker(m.reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.reapIdle()
		case <-m.reaperCtx.Done():
			return
		}
	}
}

func (m *streamBridgeManager) reapIdle() {
	now := m.now()
	var expired []*streamBridgeEntry
	m.mu.Lock()
	for instance, entry := range m.entries {
		if entry.starting || entry.active != 0 || now.Sub(entry.lastUsed) < m.idleTimeout {
			continue
		}
		delete(m.entries, instance)
		entry.closed = true
		expired = append(expired, entry)
	}
	m.mu.Unlock()
	for _, entry := range expired {
		m.closeEntry(context.Background(), entry)
	}
}

func (m *streamBridgeManager) close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	// A start publishes cmd/transport fields outside the manager lock. Wait
	// for it before reading those fields so shutdown cannot race a first-use
	// startup or miss a child that was spawned during shutdown.
	m.starts.Wait()

	m.mu.Lock()
	entries := make([]*streamBridgeEntry, 0, len(m.entries))
	for instance, entry := range m.entries {
		delete(m.entries, instance)
		entry.closed = true
		entries = append(entries, entry)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, entry := range entries {
		entry := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.closeEntry(ctx, entry)
		}()
	}
	wg.Wait()
	return nil
}

func streamBridgeProcessAlive(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	return cmd.Process.Signal(syscall.Signal(0)) == nil
}
