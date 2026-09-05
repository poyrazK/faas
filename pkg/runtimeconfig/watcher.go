// Package runtimeconfig provides the small daemon-side consumer used by
// settings that are safe to change while a process is serving traffic.
//
// The control plane owns the durable runtime_config_entries row.  A watcher
// keeps a local version watermark, applies only acknowledged rows, and
// re-reads the table after notifications, reconnects, and a repair interval.
// Notifications are deliberately treated as wake-ups rather than durable
// delivery.
package runtimeconfig

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	KeyTenantSurfaces      = "tenant_surfaces_enabled"
	KeyHSTS                = "hsts_enabled"
	KeyDataPlacement       = "data_placement_enabled"
	KeyGatewayStreaming    = "gateway_streaming_enabled"
	KeyGatewayRouteMetrics = "gateway_route_metrics_enabled"
	KeyGatewayRawStream    = "gateway_raw_stream_enabled"
)

// ApplyFunc installs one durable value into a daemon's local runtime state.
// It must be idempotent: a reconnect or a second notification may replay the
// same version.
type ApplyFunc func(ctx context.Context, key string, value json.RawMessage, version int64) error

// Acknowledger is implemented by stores that have the optional convergence
// table. Keeping it separate from state.Store lets older test doubles and
// rolling deployments continue to run while the acknowledgement migration is
// being applied.
type Acknowledger interface {
	AcknowledgeRuntimeConfig(ctx context.Context, ack state.RuntimeConfigAck) error
}

// Watcher consumes global runtime configuration rows for one daemon. Keys not
// listed in Keys are ignored, which lets each daemon subscribe to the same
// channel without accidentally applying settings it does not understand.
type Watcher struct {
	Store state.Store
	Pool  *pgxpool.Pool
	Keys  map[string]struct{}
	Apply ApplyFunc
	Log   *slog.Logger
	// Consumer and NodeID identify the daemon instance in the optional
	// runtime_config_acks table. Empty Consumer disables acknowledgement writes.
	Consumer string
	NodeID   string

	// Interval is the repair cadence. Zero uses five seconds, matching the
	// apid control-plane subscriber.
	Interval time.Duration

	mu sync.Mutex
	// versions is keyed by the complete selected target (key + scope + id),
	// while selected remembers which target won precedence for each key. A
	// scoped override can have the same version as a global row, so a single
	// per-key watermark would incorrectly skip that transition.
	versions map[string]int64
	selected map[string]string
}

// New builds a watcher for the supplied keys. A nil logger is replaced with
// slog.Default; a nil store/pool is accepted so test and degraded daemon
// wiring can leave the watcher disabled without a panic.
func New(store state.Store, pool *pgxpool.Pool, keys []string, apply ApplyFunc, log *slog.Logger) *Watcher {
	if log == nil {
		log = slog.Default()
	}
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key != "" {
			keySet[key] = struct{}{}
		}
	}
	return &Watcher{
		Store:    store,
		Pool:     pool,
		Keys:     keySet,
		Apply:    apply,
		Log:      log,
		Interval: 5 * time.Second,
		versions: make(map[string]int64),
		selected: make(map[string]string),
	}
}

// WithIdentity enables per-daemon convergence acknowledgements. NodeID may be
// empty for single-box deployments; Consumer remains the stable daemon name.
func (w *Watcher) WithIdentity(consumer, nodeID string) *Watcher {
	if w != nil {
		w.Consumer = consumer
		w.NodeID = nodeID
	}
	return w
}

// Run keeps the daemon's local snapshot converged until ctx is cancelled.
// Initial LISTEN failures are retried with bounded backoff so a transient
// database restart does not leave an edge daemon permanently stale.
func (w *Watcher) Run(ctx context.Context) error {
	if w == nil || w.Store == nil || w.Pool == nil || w.Apply == nil || len(w.Keys) == 0 {
		return nil
	}
	if w.Log == nil {
		w.Log = slog.Default()
	}
	interval := w.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	backoff := 250 * time.Millisecond
	for {
		notifications, err := db.SubscribeWithReconnect(ctx, w.Pool, []string{db.NotifyRuntimeConfigChanged}, w.Log)
		if err == nil {
			if reconcileErr := w.Reconcile(ctx); reconcileErr != nil {
				w.Log.Warn("runtime_config initial reconcile failed", "err", reconcileErr)
			}
			return w.runSubscription(ctx, notifications, interval)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.Log.Warn("runtime_config subscriber setup failed; retrying", "err", err, "backoff", backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}
	}
}

func (w *Watcher) runSubscription(ctx context.Context, notifications <-chan db.Notification, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.Reconcile(ctx); err != nil {
				w.Log.Warn("runtime_config reconcile failed", "err", err)
			}
		case _, ok := <-notifications:
			if !ok {
				return nil
			}
			if err := w.Reconcile(ctx); err != nil {
				w.Log.Warn("runtime_config reconcile failed", "err", err)
			}
		}
	}
}

// Reconcile applies every control-plane row whose effective state is marked
// applied. Rows with a pending/failed/blocked status are intentionally
// ignored; desired state is not effective state. A per-daemon acknowledgement
// is written only after the local ApplyFunc succeeds.
func (w *Watcher) Reconcile(ctx context.Context) error {
	if w == nil || w.Store == nil || w.Apply == nil || len(w.Keys) == 0 {
		return nil
	}
	// Read every scope in one query. The selected value is resolved locally so
	// a daemon can move between global, daemon, and node overrides without a
	// second control-plane round trip.
	rows, err := w.Store.ListRuntimeConfigs(ctx, "", "")
	if err != nil {
		return fmt.Errorf("list runtime config: %w", err)
	}
	var firstErr error
	for key := range w.Keys {
		row, ok := w.selectTarget(rows, key)
		if !ok {
			continue
		}
		targetKey := runtimeConfigTargetKey(row)
		w.mu.Lock()
		lastVersion := w.versions[targetKey]
		lastTarget := w.selected[row.Key]
		w.mu.Unlock()
		if lastTarget == targetKey && row.Version <= lastVersion {
			continue
		}
		value := append(json.RawMessage(nil), row.EffectiveValue...)
		if len(value) == 0 || string(value) == "null" {
			value = append(json.RawMessage(nil), row.DesiredValue...)
		}
		if err := w.Apply(ctx, row.Key, value, row.Version); err != nil {
			if ackErr := w.acknowledge(ctx, row, state.RuntimeConfigAckFailed, nil, err.Error()); ackErr != nil {
				w.Log.Warn("runtime_config failed acknowledgement", "key", row.Key, "version", row.Version, "err", ackErr)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("apply %s v%d: %w", row.Key, row.Version, err)
			}
			continue
		}
		if err := w.acknowledge(ctx, row, state.RuntimeConfigAckApplied, value, ""); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("acknowledge %s v%d: %w", row.Key, row.Version, err)
			}
			continue
		}
		w.mu.Lock()
		w.versions[targetKey] = row.Version
		w.selected[row.Key] = targetKey
		w.mu.Unlock()
	}
	return firstErr
}

// selectTarget resolves the highest-precedence applied row visible to this
// daemon. Node overrides win over daemon overrides, which win over global
// defaults. A canary that does not include this identity is skipped so the
// lower-precedence value remains live as the safe fallback.
func (w *Watcher) selectTarget(rows []state.RuntimeConfig, key string) (state.RuntimeConfig, bool) {
	var selected state.RuntimeConfig
	selectedRank := -1
	for _, row := range rows {
		if row.Key != key || row.Status != state.RuntimeConfigApplied || !w.matchesScope(row) || !w.inRollout(row) {
			continue
		}
		rank := runtimeConfigScopeRank(row.Scope)
		if rank > selectedRank {
			selected = row
			selectedRank = rank
		}
	}
	return selected, selectedRank >= 0
}

func (w *Watcher) matchesScope(row state.RuntimeConfig) bool {
	switch row.Scope {
	case state.RuntimeConfigScopeGlobal:
		return row.ScopeID == ""
	case state.RuntimeConfigScopeDaemon:
		return w.Consumer != "" && row.ScopeID == w.Consumer
	case state.RuntimeConfigScopeNode:
		return w.NodeID != "" && row.ScopeID == w.NodeID
	default:
		// control_plane values are consumed by apid, not edge watchers.
		return false
	}
}

func (w *Watcher) inRollout(row state.RuntimeConfig) bool {
	percent := row.RolloutPercent
	if percent >= 100 {
		return true
	}
	if percent <= 0 || (w.Consumer == "" && w.NodeID == "") {
		return false
	}
	identity := w.Consumer + "\x00" + w.NodeID
	// The identity hash is stable across versions. Increasing the percentage
	// therefore expands a canary monotonically instead of reshuffling nodes.
	sum := sha256.Sum256([]byte(string(row.Key) + "\x00" + string(row.Scope) + "\x00" + row.ScopeID + "\x00" + identity))
	bucket := int(sum[0]) % 100
	return bucket < percent
}

func runtimeConfigScopeRank(scope state.RuntimeConfigScope) int {
	switch scope {
	case state.RuntimeConfigScopeNode:
		return 3
	case state.RuntimeConfigScopeDaemon:
		return 2
	case state.RuntimeConfigScopeGlobal:
		return 1
	default:
		return -1
	}
}

func runtimeConfigTargetKey(row state.RuntimeConfig) string {
	return row.Key + "\x00" + string(row.Scope) + "\x00" + row.ScopeID
}

func (w *Watcher) acknowledge(ctx context.Context, row state.RuntimeConfig, status state.RuntimeConfigAckStatus, value json.RawMessage, applyErr string) error {
	if w == nil || w.Consumer == "" {
		return nil
	}
	ackStore, ok := w.Store.(Acknowledger)
	if !ok {
		return nil
	}
	var appliedAt *time.Time
	if status == state.RuntimeConfigAckApplied {
		now := time.Now().UTC()
		appliedAt = &now
	}
	return ackStore.AcknowledgeRuntimeConfig(ctx, state.RuntimeConfigAck{
		Key:            row.Key,
		Scope:          row.Scope,
		ScopeID:        row.ScopeID,
		Consumer:       w.Consumer,
		NodeID:         w.NodeID,
		Version:        row.Version,
		Status:         status,
		EffectiveValue: append(json.RawMessage(nil), value...),
		Error:          applyErr,
		AppliedAt:      appliedAt,
	})
}

// Bool decodes the JSON boolean shape used by feature flags. It is kept here
// so every daemon rejects malformed durable values consistently.
func Bool(value json.RawMessage) (bool, error) {
	var enabled bool
	if err := json.Unmarshal(value, &enabled); err != nil {
		return false, err
	}
	return enabled, nil
}

// IsContextDone identifies normal shutdown so callers can avoid logging a
// cancellation as a failed subscriber.
func IsContextDone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
