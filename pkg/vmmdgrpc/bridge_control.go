package vmmdgrpc

// Bridge control sockets in shadow (connection-survival ADR §2 "Gate and
// counters", phase P1d slice 1). With FAAS_INSTANCE_BRIDGE_CONTROL=1, vmmd
// starts each instance bridge unit with a control socket, keeps one
// connection to it, and periodically compares the bridge's counters with its
// own activity tracker. It decides nothing from the bridge's counters yet:
// the comparison (instance_bridge_activity_checks_total) shows whether they
// can replace the tracker once forwards stop passing through vmmd (P2).

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/instancebridge"
)

const (
	instanceBridgeControlEnv = "FAAS_INSTANCE_BRIDGE_CONTROL"
	bridgeControlSocketName  = "control.sock"
	bridgeControlDialTimeout = 2 * time.Second
	bridgeControlReadTimeout = time.Second
	// bridgeActivityShadowTick is how often the shadow pass compares counts.
	bridgeActivityShadowTick = 5 * time.Second
)

// Shadow comparison results.
const (
	bridgeActivityAgree      = "agree"
	bridgeActivityBridgeBusy = "bridge_only_busy" // never expected while vmmd forwards
	bridgeActivityVMMDBusy   = "vmmd_only_busy"   // expected for executions and tail tasks
	bridgeActivityError      = "error"
)

var instanceBridgeActivityChecks = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "instance_bridge_activity_checks_total",
	Help: "Shadow comparisons of an instance bridge's activity counters with vmmd's activity tracker, by result (connection-survival ADR P1d).",
}, []string{"result"})

func init() { prometheus.MustRegister(instanceBridgeActivityChecks) }

// instanceBridgeControlEnabled reports whether bridge units get a control
// socket. It needs bridge units: only they have a socket directory of their
// own.
func instanceBridgeControlEnabled() bool {
	return os.Getenv(instanceBridgeControlEnv) == "1" && InstanceBridgeUnitsEnabled()
}

// bridgeControlSocket is the control socket beside a bridge's data socket,
// in the bridge uid's own directory.
func bridgeControlSocket(dataSocket string) string {
	return filepath.Join(filepath.Dir(dataSocket), bridgeControlSocketName)
}

// bridgeControlEnv is the environment that asks a bridge for a control
// socket.
func bridgeControlEnv(entry *streamBridgeEntry) []string {
	if !instanceBridgeControlEnabled() {
		return nil
	}
	return []string{
		"FAAS_BRIDGE_CONTROL_SOCKET=" + bridgeControlSocket(entry.socket),
		"FAAS_BRIDGE_INSTANCE=" + entry.instance,
	}
}

// connectControl opens entry's control connection. The peer must be the
// bridge's uid, as for the data socket, and its hello must name entry's
// instance. A failure leaves entry without one: the shadow pass counts
// nothing for it and forwarding is unaffected.
func (m *streamBridgeManager) connectControl(ctx context.Context, entry *streamBridgeEntry) {
	if entry.unit == nil || !instanceBridgeControlEnabled() {
		return
	}
	if _, err := os.Lstat(bridgeControlSocket(entry.socket)); errors.Is(err, os.ErrNotExist) {
		// An adopted bridge started before the switch was on serves none.
		return
	}
	client, err := dialBridgeControl(ctx, entry)
	if err != nil {
		instanceBridgeActivityChecks.WithLabelValues(bridgeActivityError).Inc()
		m.log.Debug("vmmd: instance bridge control unavailable", "instance", entry.instance, "err", err)
		return
	}
	m.mu.Lock()
	if entry.closed {
		m.mu.Unlock()
		_ = client.Close()
		return
	}
	old := entry.control
	entry.control = client
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func dialBridgeControl(ctx context.Context, entry *streamBridgeEntry) (*instancebridge.ControlClient, error) {
	dialCtx, cancel := context.WithTimeout(ctx, bridgeControlDialTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", bridgeControlSocket(entry.socket))
	if err != nil {
		return nil, err
	}
	if err := entry.identity.verifyBridgePeer(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	client, err := instancebridge.NewControlClient(dialCtx, conn, entry.instance)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

// closeControl drops entry's control connection.
func (m *streamBridgeManager) closeControl(entry *streamBridgeEntry) {
	m.mu.Lock()
	client := entry.control
	entry.control = nil
	m.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

// bridgeActivity is one bridge's counters, or why they could not be read.
type bridgeActivity struct {
	instance string
	stats    instancebridge.Stats
	err      error
}

// readBridgeActivity reads the counters of every bridge with a control
// connection. Reads happen outside the manager's lock.
func (m *streamBridgeManager) readBridgeActivity(ctx context.Context) []bridgeActivity {
	type target struct {
		instance string
		client   *instancebridge.ControlClient
	}
	m.mu.Lock()
	targets := make([]target, 0, len(m.entries))
	for _, entry := range m.entries {
		if entry.control != nil && !entry.closed {
			targets = append(targets, target{entry.instance, entry.control})
		}
	}
	m.mu.Unlock()
	out := make([]bridgeActivity, 0, len(targets))
	for _, t := range targets {
		readCtx, cancel := context.WithTimeout(ctx, bridgeControlReadTimeout)
		stats, err := t.client.Stats(readCtx)
		cancel()
		out = append(out, bridgeActivity{instance: t.instance, stats: stats, err: err})
	}
	return out
}

// compareBridgeActivity classifies one bridge's counters against vmmd's
// tracker: is each side busy?
func compareBridgeActivity(bridge bridgeActivity, vmmdInflight int64) string {
	if bridge.err != nil {
		return bridgeActivityError
	}
	bridgeBusy, vmmdBusy := bridge.stats.Busy(), vmmdInflight > 0
	switch {
	case bridgeBusy == vmmdBusy:
		return bridgeActivityAgree
	case bridgeBusy:
		return bridgeActivityBridgeBusy
	default:
		return bridgeActivityVMMDBusy
	}
}

// RunInstanceBridgeActivityShadow compares bridge counters with vmmd's
// activity tracker until ctx ends. It returns at once unless bridge control
// sockets are on.
func (s *Server) RunInstanceBridgeActivityShadow(ctx context.Context) {
	if !instanceBridgeControlEnabled() || s.activity == nil || s.streamBridges == nil {
		return
	}
	ticker := time.NewTicker(bridgeActivityShadowTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, b := range s.streamBridges.readBridgeActivity(ctx) {
			inflight, _ := s.activity.Inflight(b.instance)
			result := compareBridgeActivity(b, inflight)
			instanceBridgeActivityChecks.WithLabelValues(result).Inc()
			if result == bridgeActivityBridgeBusy {
				s.log.Warn("vmmd: instance bridge reports activity vmmd does not",
					"instance", b.instance, "bridge_inflight", b.stats.Inflight, "bridge_sessions", b.stats.Sessions)
			}
		}
	}
}
