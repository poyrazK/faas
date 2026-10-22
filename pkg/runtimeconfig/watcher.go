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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	KeyTenantSurfaces = "tenant_surfaces_enabled"
	KeyHSTS           = "hsts_enabled"
	KeyDataPlacement  = "data_placement_enabled"
	KeyS3             = "s3_enabled"
	// KeyAPIMonetization releases API monetization (ADR-20261018090210).
	KeyAPIMonetization = "api_monetization_enabled"
	// KeyPlatformReleasePause is the break-glass release pause (ADR
	// 20261022140000): while true, apid refuses new deployments and starts no
	// scheduled release, and builderd claims no queued build. Rollbacks and
	// running workloads are unaffected.
	KeyPlatformReleasePause = "platform_release_pause_enabled"
)

// ApplyFunc installs one durable value into a daemon's local runtime state.
// It must be idempotent: a reconnect or a second notification may replay the
// same version.
type ApplyFunc func(ctx context.Context, key string, value json.RawMessage, version int64) error

// Store is what a Watcher reads and records. It is not state.Store, so an
// edge daemon's watcher does not bring the whole store type into its binary
// (docs/adr/20261021092138-edge-store-blast-radius.md).
type Store interface {
	ListRuntimeConfigs(ctx context.Context, scope state.RuntimeConfigScope, scopeID string) ([]state.RuntimeConfig, error)
	RecordRuntimeConfigApplyReceipt(ctx context.Context, receipt state.RuntimeConfigApplyReceipt) error
}

// Watcher consumes global runtime configuration rows for one daemon. Keys not
// listed in Keys are ignored, which lets each daemon subscribe to the same
// channel without accidentally applying settings it does not understand.
type Watcher struct {
	Store Store
	Pool  *pgxpool.Pool
	Keys  map[string]struct{}
	Apply ApplyFunc
	Log   *slog.Logger

	// Interval is the repair cadence. Zero uses five seconds, matching the
	// apid control-plane subscriber.
	Interval time.Duration

	// Daemon and Instance identify this consumer in apply receipts
	// (ADR-1700). An empty Daemon disables receipts. New fills Instance
	// from FAAS_NODE_NAME, falling back to the hostname.
	Daemon   string
	Instance string

	mu       sync.Mutex
	versions map[string]int64
	// failed is the last version whose apply failure was reported, so a
	// value that keeps failing is recorded once rather than every repair.
	failed map[string]int64
}

// New builds a watcher for the supplied keys. A nil logger is replaced with
// slog.Default; a nil store/pool is accepted so test and degraded daemon
// wiring can leave the watcher disabled without a panic.
func New(store Store, pool *pgxpool.Pool, keys []string, apply ApplyFunc, log *slog.Logger) *Watcher {
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
		Instance: DefaultInstance(),
		versions: make(map[string]int64),
		failed:   make(map[string]int64),
	}
}

// DefaultInstance names this host in apply receipts: FAAS_NODE_NAME, else the
// hostname.
func DefaultInstance() string {
	if name := strings.TrimSpace(os.Getenv("FAAS_NODE_NAME")); name != "" {
		return name
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "unknown"
}

// WithPlatformFlags extends a daemon's watched keys and apply function so the
// same watcher keeps a platform-flag set current (ADR-1700). Platform-flag
// rows go to set; every other key goes to apply, which may be nil when the
// daemon consumes only platform flags.
func WithPlatformFlags(keys []string, apply ApplyFunc, set *platformflags.Set) ([]string, ApplyFunc) {
	keys = append(append([]string(nil), keys...), platformflags.ConfigKeys()...)
	return keys, func(ctx context.Context, key string, value json.RawMessage, version int64) error {
		if platformflags.IsConfigKey(key) {
			return set.Apply(key, value, version)
		}
		if apply == nil {
			return nil
		}
		return apply(ctx, key, value, version)
	}
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

// Reconcile applies every acknowledged row for this daemon. Rows with a
// pending/failed/blocked status are intentionally ignored; desired state is
// not effective state.
func (w *Watcher) Reconcile(ctx context.Context) error {
	if w == nil || w.Store == nil || w.Apply == nil || len(w.Keys) == 0 {
		return nil
	}
	rows, err := w.Store.ListRuntimeConfigs(ctx, state.RuntimeConfigScopeGlobal, "")
	if err != nil {
		return fmt.Errorf("list runtime config: %w", err)
	}
	var firstErr error
	for _, row := range rows {
		if _, ok := w.Keys[row.Key]; !ok || row.Status != state.RuntimeConfigApplied {
			continue
		}
		w.mu.Lock()
		lastVersion := w.versions[row.Key]
		w.mu.Unlock()
		if row.Version <= lastVersion {
			continue
		}
		if err := w.Apply(ctx, row.Key, append(json.RawMessage(nil), row.EffectiveValue...), row.Version); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("apply %s v%d: %w", row.Key, row.Version, err)
			}
			w.mu.Lock()
			if w.failed == nil {
				w.failed = make(map[string]int64)
			}
			report := w.failed[row.Key] != row.Version
			w.failed[row.Key] = row.Version
			w.mu.Unlock()
			if report {
				w.recordReceipt(ctx, row, state.RuntimeConfigReceiptFailed, err.Error())
			}
			continue
		}
		w.mu.Lock()
		if row.Version > w.versions[row.Key] {
			w.versions[row.Key] = row.Version
		}
		delete(w.failed, row.Key)
		w.mu.Unlock()
		w.recordReceipt(ctx, row, state.RuntimeConfigReceiptApplied, "")
	}
	return firstErr
}

// recordReceipt reports an apply outcome. A failed write only logs: the
// receipt is evidence for the operator, never a condition for serving.
func (w *Watcher) recordReceipt(ctx context.Context, row state.RuntimeConfig, status state.RuntimeConfigApplyReceiptStatus, errText string) {
	if w.Daemon == "" || w.Store == nil {
		return
	}
	instance := w.Instance
	if instance == "" {
		instance = DefaultInstance()
	}
	err := w.Store.RecordRuntimeConfigApplyReceipt(ctx, state.RuntimeConfigApplyReceipt{
		Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID, Daemon: w.Daemon, Instance: instance,
		Version: row.Version, Status: status, Error: errText,
	})
	if err != nil && w.Log != nil {
		w.Log.Warn("runtime_config receipt write failed", "key", row.Key, "version", row.Version, "err", err)
	}
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
