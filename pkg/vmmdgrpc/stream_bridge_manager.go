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
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"golang.org/x/net/http2"
)

const (
	streamBridgeIdleTimeout     = 2 * time.Minute
	streamBridgeReapInterval    = 30 * time.Second
	streamBridgePersistentEnv   = "FAAS_STREAM_BRIDGE_PERSISTENT"
	streamBridgePersistentValue = "1"
)

type streamBridgeSpawnFunc func(context.Context, string, string, bridgeIdentity, string, string, uint32, string, []string) (*exec.Cmd, *bytes.Buffer, error)

type streamBridgeEntry struct {
	key       string
	target    *netns.PublicForwardTarget
	instance  string
	netns     string
	port      uint32
	protocol  string
	identity  bridgeIdentity
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

func (e *streamBridgeEntry) mapKey() string {
	if e.key != "" {
		return e.key
	}
	return e.instance
}

// streamBridgeStart is what prewarm needs to start an instance's bridge.
type streamBridgeStart struct {
	target      *netns.PublicForwardTarget
	identity    bridgeIdentity
	instance    string
	netns       string
	port        uint32
	appProtocol string
}

// rememberLocked records the start of an entry. The caller holds m.mu.
func (m *streamBridgeManager) rememberLocked(entry *streamBridgeEntry, appProtocol string) {
	if m.started == nil {
		m.started = make(map[string]streamBridgeStart)
	}
	m.started[entry.mapKey()] = streamBridgeStart{target: entry.target, identity: entry.identity, instance: entry.instance, netns: entry.netns,
		port: entry.port, appProtocol: appProtocol}
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
	// started holds, by entry key, what started each live instance's bridge.
	// It outlives the entry (idle reaping, a freeze) so a thaw can start the
	// bridge again beside the resume; forget drops it with the instance.
	started map[string]streamBridgeStart
	log     *slog.Logger

	idleTimeout  time.Duration
	reapInterval time.Duration
	now          func() time.Time
	spawn        streamBridgeSpawnFunc
	waitSocket   func(string, time.Duration) error
	newTransport func(string, bridgeIdentity) *http2.Transport
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
		started:      make(map[string]streamBridgeStart),
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

func (m *streamBridgeManager) acquire(ctx context.Context, req *vmmdpb.ForwardHTTPRequestInit, netnsName string, id bridgeIdentity) (*streamBridgeLease, error) {
	if m == nil {
		return nil, errors.New("stream bridge manager is nil")
	}
	// The reaper belongs to the VMMD server, not to this request. It is
	// canceled by Server.Close, so it intentionally outlives ctx.
	m.startReaper(ctx)
	m.mu.Lock()
	// A persistent bridge is owned by this manager, not by the request that
	// happened to start it. Passing ctx here would make exec.CommandContext
	// kill the bridge as soon as that request's gRPC stream closes, leaving a
	// stale entry and socket for the next request. The reaper context is
	// canceled only by manager shutdown and therefore matches the child
	// lifetime exactly.
	bridgeCtx := m.reaperCtx
	m.mu.Unlock()
	key := streamBridgeEntryKey(req.GetInstance(), publicTarget(ctx))
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("stream bridge manager is closed")
		}
		entry := m.entries[key]
		if entry == nil {
			port := req.GetPort()
			if port == 0 {
				port = netns.AppPort
			}
			entry = &streamBridgeEntry{
				key:      key,
				target:   publicTarget(ctx),
				instance: req.GetInstance(),
				netns:    netnsName,
				port:     port,
				protocol: streamBridgeProtocol(req),
				identity: id,
				socket:   streamBridgeSockPath(key, id),
				ready:    make(chan struct{}),
				starting: true,
				lastUsed: m.now(),
			}
			m.entries[entry.mapKey()] = entry
			m.rememberLocked(entry, req.GetAppProtocol())
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
		port := req.GetPort()
		if port == 0 {
			port = netns.AppPort
		}
		if entry.netns != netnsName || entry.port != port || entry.protocol != streamBridgeProtocol(req) || entry.identity != id || !samePublicTarget(entry.target, publicTarget(ctx)) {
			// An instance ID can be reused after park/destroy and wake. Never
			// send a new request through a bridge that is attached to the old
			// network namespace or runs as the old lease's bridge uid, even if
			// its child has not exited yet.
			delete(m.entries, entry.mapKey())
			entry.closed = true
			m.mu.Unlock()
			m.closeEntry(context.WithoutCancel(ctx), entry)
			continue
		}
		if !streamBridgeProcessAlive(entry.cmd) {
			delete(m.entries, entry.mapKey())
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

// prewarm publishes a starting entry before returning, then performs the
// process/socket startup in the background. A first request racing the startup
// joins the same ready channel in acquire instead of spawning a second bridge.
//
//nolint:contextcheck // Startup uses the stored manager context derived by startReaper; it outlives RPC cancellation and manager.close cancels it.
func (m *streamBridgeManager) prewarm(ctx context.Context, req *vmmdpb.ForwardHTTPRequestInit, netnsName string, id bridgeIdentity) {
	if m == nil || req == nil || req.GetInstance() == "" || netnsName == "" {
		return
	}
	m.startReaper(ctx)
	port := req.GetPort()
	if port == 0 {
		port = netns.AppPort
	}
	protocol := streamBridgeProtocol(req)
	key := streamBridgeEntryKey(req.GetInstance(), publicTarget(ctx))
	req = &vmmdpb.ForwardHTTPRequestInit{
		Instance:    req.GetInstance(),
		Port:        port,
		AppProtocol: req.GetAppProtocol(),
	}
	m.mu.Lock()
	if m.closed || m.entries[key] != nil {
		m.mu.Unlock()
		return
	}
	entry := &streamBridgeEntry{
		key:      key,
		target:   publicTarget(ctx),
		instance: req.GetInstance(),
		netns:    netnsName,
		port:     port,
		protocol: protocol,
		identity: id,
		socket:   streamBridgeSockPath(key, id),
		ready:    make(chan struct{}),
		starting: true,
		lastUsed: m.now(),
	}
	m.entries[entry.mapKey()] = entry
	m.rememberLocked(entry, req.GetAppProtocol())
	bridgeCtx := m.reaperCtx
	m.starts.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.starts.Done()
		if err := m.startEntry(bridgeCtx, entry, req); err != nil {
			m.log.Warn("vmmd: stream bridge prewarm failed", "instance", entry.instance, "netns", entry.netns, "err", err)
		}
	}()
}

// forget removes an instance bridge after park/destroy or a failed wake. It is
// safe while prewarm is still starting: startEntry observes entry.closed and
// tears down any child it managed to create.
func (m *streamBridgeManager) forget(ctx context.Context, instance string) {
	m.closeInstance(ctx, instance, true)
}

// suspend closes an instance's bridges while its guest is frozen (ADR-1151)
// and keeps what started them, so resume can start them again at the thaw.
func (m *streamBridgeManager) suspend(ctx context.Context, instance string) {
	m.closeInstance(ctx, instance, false)
}

// resume starts again, in the background, every bridge the instance had
// before suspend or idle reaping closed it. The first request after a thaw
// then joins a bridge that started beside the resume instead of spawning one
// on its own path.
func (m *streamBridgeManager) resume(ctx context.Context, instance string) int {
	if m == nil || instance == "" {
		return 0
	}
	m.mu.Lock()
	var starts []streamBridgeStart
	for _, start := range m.started {
		if start.instance == instance {
			starts = append(starts, start)
		}
	}
	m.mu.Unlock()
	for _, start := range starts {
		m.prewarm(withPublicTarget(ctx, start.target), &vmmdpb.ForwardHTTPRequestInit{
			Instance: start.instance, Port: start.port, AppProtocol: start.appProtocol,
		}, start.netns, start.identity)
	}
	return len(starts)
}

func (m *streamBridgeManager) closeInstance(ctx context.Context, instance string, dropStarts bool) {
	if m == nil || instance == "" {
		return
	}
	m.mu.Lock()
	if dropStarts {
		for key, start := range m.started {
			if start.instance == instance {
				delete(m.started, key)
			}
		}
	}
	var stopped []*streamBridgeEntry
	for key, entry := range m.entries {
		if entry.instance != instance {
			continue
		}
		delete(m.entries, key)
		entry.closed = true
		if !entry.starting {
			stopped = append(stopped, entry)
		}
	}
	m.mu.Unlock()
	// Starting entries own cleanup after publishing their startup result.
	for _, entry := range stopped {
		m.closeEntry(ctx, entry)
	}
}

func (m *streamBridgeManager) startEntry(ctx context.Context, entry *streamBridgeEntry, req *vmmdpb.ForwardHTTPRequestInit) error {
	err := m.startEntryProcess(ctx, entry, req)
	m.mu.Lock()
	entry.startErr = err
	entry.starting = false
	if err != nil || m.closed {
		delete(m.entries, entry.mapKey())
		entry.closed = true
	}
	shouldClose := err != nil || entry.closed
	close(entry.ready)
	m.mu.Unlock()
	if shouldClose {
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
	if entry.identity.unprivileged() {
		if err := entry.identity.prepareSocketDir(entry.socket); err != nil {
			return fmt.Errorf("stream bridge socket: %w", err)
		}
	} else if err := os.Remove(entry.socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale stream bridge socket: %w", err)
	}
	deadline := m.now().Add(streamBridgeSessionDeadline).UTC().Format(time.RFC3339)
	spawnNetns, guestIP := streamBridgeTarget(withPublicTarget(ctx, entry.target), entry.netns)
	env := netns.PublicForwardEnv(streamBridgeStaticEnv(req), entry.target)
	cmd, stderr, err := m.spawn(ctx, bridgePath, spawnNetns, entry.identity, entry.socket, guestIP, port, deadline, env)
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
	transport := m.newTransport(entry.socket, entry.identity)
	m.mu.Lock()
	if entry.closed {
		m.mu.Unlock()
		transport.CloseIdleConnections()
		return fmt.Errorf("stream bridge exited before becoming ready: %w", entry.waitError())
	}
	entry.transport = transport
	entry.client = newGuestHTTPClient(transport)
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
	current := m.entries[entry.mapKey()] == entry && !entry.closed
	unexpected := current && !m.closed
	if current {
		delete(m.entries, entry.mapKey())
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
	entry.identity.removeSocket(socket)
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
	if current := m.entries[entry.mapKey()]; current != entry {
		m.mu.Unlock()
		return
	}
	delete(m.entries, entry.mapKey())
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
	entry.identity.removeSocket(entry.socket)
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

func (m *streamBridgeManager) startReaper(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.reaperOnce.Do(func() {
		reaperCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		m.reaperCtx, m.cancel = reaperCtx, cancel
		go m.reapLoop(reaperCtx)
	})
}

func (m *streamBridgeManager) reapLoop(ctx context.Context) {
	ticker := time.NewTicker(m.reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.reapIdle(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (m *streamBridgeManager) reapIdle(ctx context.Context) {
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
		m.closeEntry(ctx, entry)
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

// prewarmStreamBridge starts an instance's persistent bridge while it is
// still waking. The instance is not live yet, so its bridge identity comes
// from the lease's jail uid in the network-ready event. A failure only skips
// the prewarm: the first request starts the bridge instead.
func (s *Server) prewarmStreamBridge(ctx context.Context, req *vmmdpb.ForwardHTTPRequestInit, ready fcvm.WakeNetworkReady) {
	// A public-target bridge keeps the root identity (forwardBridgeIdentity).
	var id bridgeIdentity
	var err error
	if publicTarget(ctx) == nil {
		id, err = bridgeIdentityForJailUID(ready.JailUID)
	}
	if err != nil {
		s.log.Warn("vmmd: stream bridge prewarm skipped", "instance", ready.Instance, "err", err)
		return
	}
	s.streamBridges.prewarm(ctx, req, ready.Netns, id)
}
